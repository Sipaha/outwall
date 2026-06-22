package k8s

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Sipaha/outwall/internal/upstream"
)

// Importer registers kubeconfig contexts as kind=k8s upstreams. Two modes (see ADR-0026):
//   - update=false (the init auto-scan): a context whose name already names an upstream is skipped,
//     never mutated — so re-running the scan never clobbers operator state.
//   - update=true (an explicit operator import/upload): an existing cluster is refreshed in place
//     (server + auth on the same upstream ID, preserving its rules), so the operator can repair or
//     rotate credentials.
type Importer struct {
	Reg *upstream.Registry
	Log *slog.Logger
}

// Import reads each existing path, parses every context, and registers them. Missing files are
// skipped silently (not every discovered path exists). Returns the context names added, updated
// (only when update=true), and skipped (already-present, update=false). A failure to register
// (e.g. the vault is locked, so the auth cannot be encrypted) returns a wrapped error after the
// names handled so far — the init caller treats it as best-effort. parse warnings are logged.
func (im *Importer) Import(paths []string, update bool) (added, updated, skipped []string, err error) {
	log := im.logger()

	// Non-nil from the start: a nil slice encodes to JSON `null`, which the UI's
	// res.added.length / res.skipped.length then throws on (false "Failed to import" toast).
	added, updated, skipped = []string{}, []string{}, []string{}

	have, err := im.existing()
	if err != nil {
		return added, updated, skipped, err
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
		if regErr := im.register(log, clusters, have, update, &added, &updated, &skipped); regErr != nil {
			return added, updated, skipped, regErr
		}
	}
	return added, updated, skipped, nil
}

// ImportContent parses one kubeconfig document (uploaded by the operator via the file picker) and
// registers its contexts. baseDir resolves any file-path refs in the document ("" = none;
// managed-cluster configs use inline *-data and need no baseDir). Returns non-nil
// added/updated/skipped. A document that does not parse as a kubeconfig is a real error here
// (unlike the auto-scan, the operator explicitly chose this file). The operator path passes
// update=true so a re-upload refreshes an existing cluster.
func (im *Importer) ImportContent(data []byte, baseDir string, update bool) (added, updated, skipped []string, err error) {
	log := im.logger()
	added, updated, skipped = []string{}, []string{}, []string{}

	have, err := im.existing()
	if err != nil {
		return added, updated, skipped, err
	}
	clusters, warnings, parseErr := ParseKubeconfig(data, baseDir)
	if parseErr != nil {
		return added, updated, skipped, fmt.Errorf("parse uploaded kubeconfig: %w", parseErr)
	}
	for _, w := range warnings {
		log.Warn("kubeconfig import: skipped context", "reason", w)
	}
	if regErr := im.register(log, clusters, have, update, &added, &updated, &skipped); regErr != nil {
		return added, updated, skipped, regErr
	}
	return added, updated, skipped, nil
}

func (im *Importer) logger() *slog.Logger {
	if im.Log != nil {
		return im.Log
	}
	return slog.Default()
}

// existing returns the upstreams already registered, keyed by name (for skip / update-in-place).
func (im *Importer) existing() (map[string]*upstream.Upstream, error) {
	ups, err := im.Reg.List()
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	have := make(map[string]*upstream.Upstream, len(ups))
	for _, u := range ups {
		have[u.Name] = u
	}
	return have, nil
}

// register creates every parsed cluster not already present; a name that already exists is updated
// in place (update=true) or skipped (update=false). It appends to added/updated/skipped and keeps
// have current. Shared body of Import and ImportContent.
func (im *Importer) register(log *slog.Logger, clusters []ParsedCluster, have map[string]*upstream.Upstream, update bool, added, updated, skipped *[]string) error {
	for _, c := range clusters {
		// SECURITY: surface a disabled-verification cluster loudly. The flag came from an
		// explicit insecure-skip-tls-verify:true in the operator's own kubeconfig.
		if c.Auth.K8sInsecureSkipVerify {
			log.Warn("cluster registered with TLS verification DISABLED from its kubeconfig",
				"cluster", c.Name, "server", c.Server)
		}
		if cur, ok := have[c.Name]; ok {
			if !update {
				*skipped = append(*skipped, c.Name)
				continue
			}
			// Refresh server + auth on the SAME upstream ID so the cluster's rules survive.
			if err := im.Reg.UpdateTarget(cur.ID, c.Server, c.Auth); err != nil {
				return fmt.Errorf("update cluster %q: %w", c.Name, err)
			}
			*updated = append(*updated, c.Name)
			continue
		}
		created, createErr := im.Reg.CreateKind(c.Name, c.Server, upstream.KindK8s, c.Auth)
		if createErr != nil {
			return fmt.Errorf("register cluster %q: %w", c.Name, createErr)
		}
		have[c.Name] = created
		*added = append(*added, c.Name)
	}
	return nil
}
