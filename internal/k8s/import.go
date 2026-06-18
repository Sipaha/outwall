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
	log := im.logger()

	// Non-nil from the start: a nil slice encodes to JSON `null`, which the UI's
	// res.added.length / res.skipped.length then throws on (false "Failed to import" toast).
	added, skipped = []string{}, []string{}

	have, err := im.existingNames()
	if err != nil {
		return added, skipped, err
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
			// A discovered file that is not a kubeconfig (junk in ~/.kube) is skipped, never fatal.
			log.Warn("kubeconfig import: not a kubeconfig, skipping", "path", path, "err", parseErr)
			continue
		}
		for _, w := range warnings {
			log.Warn("kubeconfig import: skipped context", "path", path, "reason", w)
		}
		if regErr := im.register(log, clusters, have, &added, &skipped); regErr != nil {
			return added, skipped, regErr
		}
	}
	return added, skipped, nil
}

// ImportContent parses one kubeconfig document (uploaded by the operator via the file picker) and
// registers its contexts idempotently. baseDir resolves any file-path refs in the document
// ("" = none; managed-cluster configs use inline *-data and need no baseDir). Returns non-nil
// added/skipped. A document that does not parse as a kubeconfig is a real error here (unlike the
// auto-scan, the operator explicitly chose this file).
func (im *Importer) ImportContent(data []byte, baseDir string) (added, skipped []string, err error) {
	log := im.logger()
	added, skipped = []string{}, []string{}

	have, err := im.existingNames()
	if err != nil {
		return added, skipped, err
	}
	clusters, warnings, parseErr := ParseKubeconfig(data, baseDir)
	if parseErr != nil {
		return added, skipped, fmt.Errorf("parse uploaded kubeconfig: %w", parseErr)
	}
	for _, w := range warnings {
		log.Warn("kubeconfig import: skipped context", "reason", w)
	}
	if regErr := im.register(log, clusters, have, &added, &skipped); regErr != nil {
		return added, skipped, regErr
	}
	return added, skipped, nil
}

func (im *Importer) logger() *slog.Logger {
	if im.Log != nil {
		return im.Log
	}
	return slog.Default()
}

// existingNames returns the set of upstream names already registered (for skip-existing).
func (im *Importer) existingNames() (map[string]struct{}, error) {
	existing, err := im.Reg.List()
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	have := make(map[string]struct{}, len(existing))
	for _, u := range existing {
		have[u.Name] = struct{}{}
	}
	return have, nil
}

// register creates every parsed cluster whose name is not already present, appending to added /
// skipped and updating have. It is the shared idempotent body of Import and ImportContent.
func (im *Importer) register(log *slog.Logger, clusters []ParsedCluster, have map[string]struct{}, added, skipped *[]string) error {
	for _, c := range clusters {
		if _, ok := have[c.Name]; ok {
			*skipped = append(*skipped, c.Name)
			continue
		}
		// SECURITY: surface a disabled-verification cluster loudly. The flag came from an
		// explicit insecure-skip-tls-verify:true in the operator's own kubeconfig.
		if c.Auth.K8sInsecureSkipVerify {
			log.Warn("cluster registered with TLS verification DISABLED from its kubeconfig",
				"cluster", c.Name, "server", c.Server)
		}
		if _, createErr := im.Reg.CreateKind(c.Name, c.Server, upstream.KindK8s, c.Auth); createErr != nil {
			return fmt.Errorf("register cluster %q: %w", c.Name, createErr)
		}
		have[c.Name] = struct{}{}
		*added = append(*added, c.Name)
	}
	return nil
}
