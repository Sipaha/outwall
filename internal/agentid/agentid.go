// Package agentid resolves and persists the per-project agent token used by the outwall CLI.
//
// A project is identified by the realpath of its git top-level (when cwd is inside a repo) else the
// realpath of cwd. The token for a project lives at <DataDir>/agents/<hex-sha256(projectKey)>.token
// (0600). Using the git top-level means a `cd` into a subdirectory of a repo keeps the same
// identity — one agent per project, not per directory. The token is accountability-only: a same-user
// process can read any project's token, so this is NOT an isolation boundary (see ADR-0040).
package agentid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Sipaha/outwall/internal/config"
)

// projectKey returns the stable identity key for cwd: the realpath of the git top-level when cwd is
// inside a repo, else the realpath (symlinks resolved) of cwd itself.
func projectKey(cwd string) (string, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	if out, err := cmd.Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			real, rerr := filepath.EvalSymlinks(top)
			if rerr != nil {
				return "", fmt.Errorf("resolve git top-level: %w", rerr)
			}
			return real, nil
		}
	}
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return real, nil
}

// tokenPathForKey builds the token-file path for a resolved project key.
func tokenPathForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(config.DataDir(), "agents", hex.EncodeToString(sum[:])+".token")
}

// TokenPath returns the token-file path for the project containing cwd.
func TokenPath(cwd string) (string, error) {
	key, err := projectKey(cwd)
	if err != nil {
		return "", err
	}
	return tokenPathForKey(key), nil
}
