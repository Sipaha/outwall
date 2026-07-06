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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

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

// LoadOrRegister returns the per-project agent token, minting it once on first use. It serializes
// concurrent first-calls with an exclusive flock on <tokenpath>.lock so exactly one agent is
// registered: the winner calls register and writes the token atomically; losers block on the flock
// and then read the file. register receives the basename of the project key as the agent name.
func LoadOrRegister(cwd string, register func(name string) (id, token string, err error)) (string, error) {
	key, err := projectKey(cwd)
	if err != nil {
		return "", err
	}
	path := tokenPathForKey(key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create agents dir: %w", err)
	}

	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("open lock: %w", err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) }()

	// Fast path: a token was already minted for this project (by us or an earlier flock holder).
	if b, rerr := os.ReadFile(path); rerr == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	} else if !errors.Is(rerr, os.ErrNotExist) {
		return "", fmt.Errorf("read token: %w", rerr)
	}

	// Mint once, then write atomically (temp file in the same dir + rename).
	_, token, err := register(filepath.Base(key))
	if err != nil {
		return "", fmt.Errorf("register agent: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return "", fmt.Errorf("create temp token: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(token); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write temp token: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("chmod temp token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close temp token: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("rename token: %w", err)
	}
	return token, nil
}
