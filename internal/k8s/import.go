package k8s

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Sipaha/outwall/internal/upstream"
)

// Importer registers kubeconfig contexts as kind=k8s upstreams. It is idempotent: a context
// whose name already names an upstream is skipped, never re-created or mutated.
type Importer struct {
	Reg *upstream.Registry
	Log *slog.Logger
}

// Import reads each existing path, parses every context, and registers the ones not already
// present. Missing files are skipped silently (not every discovered path exists). Returns the
// context names added and the ones skipped because they already existed. A failure to register
// (e.g. the vault is locked, so the auth cannot be encrypted) returns a wrapped error after the
// names added so far — the caller (the unlock hook) treats it as best-effort. parse warnings are
// logged, not fatal.
func (im *Importer) Import(paths []string) (added, skipped []string, err error) {
	log := im.Log
	if log == nil {
		log = slog.Default()
	}

	existing, err := im.Reg.List()
	if err != nil {
		return nil, nil, fmt.Errorf("list upstreams: %w", err)
	}
	have := make(map[string]struct{}, len(existing))
	for _, u := range existing {
		have[u.Name] = struct{}{}
	}

	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue // a discovered-but-absent path is fine
			}
			log.Warn("kubeconfig import: cannot read file", "path", path, "err", readErr)
			continue
		}
		clusters, warnings, parseErr := ParseKubeconfig(data, filepath.Dir(path))
		if parseErr != nil {
			log.Warn("kubeconfig import: parse failed", "path", path, "err", parseErr)
			continue
		}
		for _, w := range warnings {
			log.Warn("kubeconfig import: skipped context", "path", path, "reason", w)
		}
		for _, c := range clusters {
			if _, ok := have[c.Name]; ok {
				skipped = append(skipped, c.Name)
				continue
			}
			// SECURITY: surface a disabled-verification cluster loudly. The flag came from an
			// explicit insecure-skip-tls-verify:true in the operator's own kubeconfig.
			if c.Auth.K8sInsecureSkipVerify {
				log.Warn("cluster registered with TLS verification DISABLED from its kubeconfig",
					"cluster", c.Name, "server", c.Server)
			}
			if _, createErr := im.Reg.CreateKind(c.Name, c.Server, upstream.KindK8s, c.Auth); createErr != nil {
				return added, skipped, fmt.Errorf("register cluster %q: %w", c.Name, createErr)
			}
			have[c.Name] = struct{}{}
			added = append(added, c.Name)
		}
	}
	return added, skipped, nil
}
