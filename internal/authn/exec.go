package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// execTimeout bounds how long a credential plugin may run.
const execTimeout = 30 * time.Second

// execTokenSource runs an operator-configured exec credential plugin (e.g. `aws eks
// get-token`, `gke-gcloud-auth-plugin`, `kubelogin`) and caches its short-lived token.
//
// The command/args/env come ONLY from the operator-registered cluster config — never from
// any agent input — so this is a trusted-input boundary (see ADR-0008). Runs are bounded by
// execTimeout; stdout is parsed as the k8s ExecCredential JSON; the token is cached until
// ~earlyRefresh before its expiry.
type execTokenSource struct {
	command string
	args    []string
	env     []string // "K=V" entries appended to the inherited process env

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// newExecTokenSource builds an exec token source. env is a map of extra environment vars.
func newExecTokenSource(command string, args []string, env map[string]string) *execTokenSource {
	var envv []string
	for k, v := range env {
		envv = append(envv, k+"="+v)
	}
	return &execTokenSource{command: command, args: args, env: envv}
}

// execCredential is the k8s client.authentication.k8s.io ExecCredential shape (status only).
type execCredential struct {
	Status struct {
		Token               string `json:"token"`
		ExpirationTimestamp string `json:"expirationTimestamp"`
	} `json:"status"`
}

// Token returns a cached token if still valid, else runs the plugin once and caches the result.
func (e *execTokenSource) Token(ctx context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.token != "" && time.Now().Before(e.expiry.Add(-earlyRefresh)) {
		return e.token, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, e.command, e.args...)
	if len(e.env) > 0 {
		cmd.Env = append(cmd.Environ(), e.env...)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("exec credential plugin %q: %w", e.command, err)
	}
	var cred execCredential
	if err := json.Unmarshal(out, &cred); err != nil {
		return "", fmt.Errorf("parse ExecCredential from %q: %w", e.command, err)
	}
	if cred.Status.Token == "" {
		return "", fmt.Errorf("exec credential plugin %q returned an empty token", e.command)
	}

	e.token = cred.Status.Token
	if ts := cred.Status.ExpirationTimestamp; ts != "" {
		if exp, perr := time.Parse(time.RFC3339, ts); perr == nil {
			e.expiry = exp
		}
	}
	if e.expiry.IsZero() {
		// No expiry given: be conservative, refresh next time.
		e.expiry = time.Now().Add(time.Minute)
	}
	// A past-expiry token is returned this once but not effectively cached: the next call
	// re-runs the plugin because time.Now() is already past expiry-earlyRefresh.
	return e.token, nil
}
