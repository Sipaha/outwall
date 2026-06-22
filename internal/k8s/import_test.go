package k8s

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
	"github.com/Sipaha/outwall/internal/upstream"
)

func newReg(t *testing.T) (*store.Store, *secret.Vault, *upstream.Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "imp.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	v := secret.NewVault(s)
	require.NoError(t, v.Init("pw")) // Init leaves the vault unlocked
	return s, v, upstream.NewRegistry(s, v)
}

// writeKubeconfig writes a two-context kubeconfig (token + insecure) to a temp file and returns
// its path.
func writeKubeconfig(t *testing.T) string {
	t.Helper()
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: a-cluster
    cluster:
      server: https://a.example:6443
      certificate-authority-data: ` + b64(fakeCAPEM) + `
  - name: b-cluster
    cluster:
      server: https://b.example:6443
      insecure-skip-tls-verify: true
users:
  - name: a-user
    user:
      token: a-token
  - name: b-user
    user:
      token: b-token
contexts:
  - name: a-ctx
    context:
      cluster: a-cluster
      user: a-user
  - name: b-ctx
    context:
      cluster: b-cluster
      user: b-user
`
	p := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
	return p
}

func TestImporterIdempotent(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	path := writeKubeconfig(t)

	added, _, skipped, err := im.Import([]string{path}, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a-ctx", "b-ctx"}, added)
	require.Empty(t, skipped)

	// The clusters are registered as kind=k8s with the right auth.
	a, err := reg.GetByName("a-ctx")
	require.NoError(t, err)
	require.Equal(t, upstream.KindK8s, a.Kind)
	require.Equal(t, "https://a.example:6443", a.BaseURL)
	require.Equal(t, "a-token", a.Auth.Token)
	require.Equal(t, fakeCAPEM, a.Auth.CABundle)

	b, err := reg.GetByName("b-ctx")
	require.NoError(t, err)
	require.True(t, b.Auth.K8sInsecureSkipVerify)

	// Second import: nothing added, both skipped (idempotent).
	added2, _, skipped2, err := im.Import([]string{path}, false)
	require.NoError(t, err)
	require.Empty(t, added2)
	require.ElementsMatch(t, []string{"a-ctx", "b-ctx"}, skipped2)

	// Still exactly two clusters in the registry.
	ups, err := reg.List()
	require.NoError(t, err)
	require.Len(t, ups, 2)
}

func TestImporterMissingPathSkipped(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// A non-existent path is silently skipped (not every discovered path exists).
	added, _, skipped, err := im.Import([]string{filepath.Join(t.TempDir(), "does-not-exist")}, false)
	require.NoError(t, err)
	require.Empty(t, added)
	require.Empty(t, skipped)
}

func TestImporterLockedVaultErrors(t *testing.T) {
	_, v, reg := newReg(t)
	v.Lock()
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	path := writeKubeconfig(t)

	added, _, _, err := im.Import([]string{path}, false)
	require.Error(t, err, "a locked vault cannot encrypt new auth → wrapped error")
	require.ErrorIs(t, err, secret.ErrLocked)
	require.Empty(t, added)
}

// TestImporterScansKubeDirAndSkipsJunk imports every kubeconfig file in a ~/.kube-like dir: two
// separate files each carrying a distinct context, plus a non-kubeconfig "junk" file and a subdir
// — both contexts register, the junk is skipped with no error.
func TestImporterScansKubeDirAndSkipsJunk(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	kubeDir := t.TempDir()
	cfg := `
apiVersion: v1
kind: Config
clusters: [{ name: c1, cluster: { server: https://one.example:6443, insecure-skip-tls-verify: true } }]
users: [{ name: u1, user: { token: t1 } }]
contexts: [{ name: ctx-one, context: { cluster: c1, user: u1 } }]
`
	extra := `
apiVersion: v1
kind: Config
clusters: [{ name: c2, cluster: { server: https://two.example:6443, insecure-skip-tls-verify: true } }]
users: [{ name: u2, user: { token: t2 } }]
contexts: [{ name: ctx-two, context: { cluster: c2, user: u2 } }]
`
	require.NoError(t, os.WriteFile(filepath.Join(kubeDir, "config"), []byte(cfg), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(kubeDir, "extra.yaml"), []byte(extra), 0o600))
	// Junk: parses as YAML but has no clusters/contexts — skipped, never fatal.
	require.NoError(t, os.WriteFile(filepath.Join(kubeDir, "notes.txt"), []byte("just some notes\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(kubeDir, "cache"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(kubeDir, "cache", "x"), []byte("nope"), 0o600))

	added, _, skipped, err := im.Import(discoverKubeconfigPathsIn(kubeDir, ""), false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ctx-one", "ctx-two"}, added)
	require.Empty(t, skipped)

	ups, err := reg.List()
	require.NoError(t, err)
	require.Len(t, ups, 2, "only the two real contexts register; notes.txt and the subdir are ignored")
}

// TestImporterImportContent registers contexts from an uploaded kubeconfig body, idempotently.
func TestImporterImportContent(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	src := `
apiVersion: v1
kind: Config
clusters: [{ name: up-c, cluster: { server: https://up.example:6443, insecure-skip-tls-verify: true } }]
users: [{ name: up-u, user: { token: up-token } }]
contexts: [{ name: uploaded-ctx, context: { cluster: up-c, user: up-u } }]
`
	added, _, skipped, err := im.ImportContent([]byte(src), "", false)
	require.NoError(t, err)
	require.Equal(t, []string{"uploaded-ctx"}, added)
	require.Empty(t, skipped)
	require.NotNil(t, added)
	require.NotNil(t, skipped)

	c, err := reg.GetByName("uploaded-ctx")
	require.NoError(t, err)
	require.Equal(t, upstream.KindK8s, c.Kind)
	require.Equal(t, "up-token", c.Auth.Token)

	// Idempotent: a second upload of the same body skips, adds nothing.
	added2, _, skipped2, err := im.ImportContent([]byte(src), "", false)
	require.NoError(t, err)
	require.Empty(t, added2)
	require.Equal(t, []string{"uploaded-ctx"}, skipped2)
}

// TestImporterUpdatesOnReimport: with update=true an existing cluster is refreshed in place —
// same upstream ID (so its rules survive), new server + auth. This is the repair/rotate path.
func TestImporterUpdatesOnReimport(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	v1 := `
apiVersion: v1
kind: Config
clusters: [{ name: cc, cluster: { server: https://old.example:6443, insecure-skip-tls-verify: true } }]
users: [{ name: cu, user: { token: old-token } }]
contexts: [{ name: cc-ctx, context: { cluster: cc, user: cu } }]
`
	added, updated, _, err := im.ImportContent([]byte(v1), "", true)
	require.NoError(t, err)
	require.Equal(t, []string{"cc-ctx"}, added)
	require.Empty(t, updated)
	before, err := reg.GetByName("cc-ctx")
	require.NoError(t, err)

	// Re-import the SAME context name with a new server + token and update=true.
	v2 := `
apiVersion: v1
kind: Config
clusters: [{ name: cc, cluster: { server: https://new.example:6443, insecure-skip-tls-verify: true } }]
users: [{ name: cu, user: { token: new-token } }]
contexts: [{ name: cc-ctx, context: { cluster: cc, user: cu } }]
`
	added2, updated2, skipped2, err := im.ImportContent([]byte(v2), "", true)
	require.NoError(t, err)
	require.Empty(t, added2)
	require.Empty(t, skipped2)
	require.Equal(t, []string{"cc-ctx"}, updated2)

	after, err := reg.GetByName("cc-ctx")
	require.NoError(t, err)
	require.Equal(t, before.ID, after.ID, "update must keep the same upstream ID so rules survive")
	require.Equal(t, "https://new.example:6443", after.BaseURL)
	require.Equal(t, "new-token", after.Auth.Token)
	require.Equal(t, "token", after.Auth.K8sAuth)
}

// TestImporterImportContentRejectsJunk: an explicitly-uploaded non-kubeconfig is a real error
// (unlike the auto-scan, which silently skips junk in ~/.kube).
func TestImporterImportContentRejectsJunk(t *testing.T) {
	_, _, reg := newReg(t)
	im := &Importer{Reg: reg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	added, _, skipped, err := im.ImportContent([]byte("not a kubeconfig: ["), "", false)
	require.Error(t, err)
	require.Empty(t, added)
	require.Empty(t, skipped)
	require.NotNil(t, added)
}
