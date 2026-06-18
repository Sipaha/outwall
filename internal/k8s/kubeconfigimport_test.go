package k8s

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeCAPEM is a syntactically-fine PEM blob; the parser only carries the bytes through (it
// does not validate them), so any non-empty PEM string round-trips for the assertions.
const fakeCAPEM = `-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJAKZ7Dummy
-----END CERTIFICATE-----
`

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestParseKubeconfigMultiContext(t *testing.T) {
	caB64 := b64(fakeCAPEM)
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: tok-cluster
    cluster:
      server: https://tok.example:6443
      certificate-authority-data: ` + caB64 + `
  - name: cert-cluster
    cluster:
      server: https://cert.example:6443
      certificate-authority-data: ` + caB64 + `
  - name: exec-cluster
    cluster:
      server: https://exec.example:6443
      certificate-authority-data: ` + caB64 + `
users:
  - name: tok-user
    user:
      token: my-bearer-token
  - name: cert-user
    user:
      client-certificate-data: ` + b64("CLIENT-CERT-PEM") + `
      client-key-data: ` + b64("CLIENT-KEY-PEM") + `
  - name: exec-user
    user:
      exec:
        command: aws
        args:
          - eks
          - get-token
          - --cluster-name
          - prod
        env:
          - name: AWS_PROFILE
            value: prod
contexts:
  - name: tok-ctx
    context:
      cluster: tok-cluster
      user: tok-user
  - name: cert-ctx
    context:
      cluster: cert-cluster
      user: cert-user
  - name: exec-ctx
    context:
      cluster: exec-cluster
      user: exec-user
`
	clusters, warnings, err := ParseKubeconfig([]byte(src), t.TempDir())
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, clusters, 3)

	byName := map[string]ParsedCluster{}
	for _, c := range clusters {
		byName[c.Name] = c
	}

	tok := byName["tok-ctx"]
	require.Equal(t, "https://tok.example:6443", tok.Server)
	require.Equal(t, "token", tok.Auth.K8sAuth)
	require.Equal(t, "my-bearer-token", tok.Auth.Token)
	require.Equal(t, fakeCAPEM, tok.Auth.CABundle, "certificate-authority-data must be base64-decoded to PEM")

	cert := byName["cert-ctx"]
	require.Equal(t, "client-cert", cert.Auth.K8sAuth)
	require.Equal(t, "CLIENT-CERT-PEM", cert.Auth.ClientCert)
	require.Equal(t, "CLIENT-KEY-PEM", cert.Auth.ClientKey)
	require.Equal(t, fakeCAPEM, cert.Auth.CABundle)

	ex := byName["exec-ctx"]
	require.Equal(t, "exec", ex.Auth.K8sAuth)
	require.Equal(t, "aws", ex.Auth.ExecCommand)
	require.Equal(t, []string{"eks", "get-token", "--cluster-name", "prod"}, ex.Auth.ExecArgs)
	require.Equal(t, map[string]string{"AWS_PROFILE": "prod"}, ex.Auth.ExecEnv)
}

func TestParseKubeconfigResolvesCAFileRelativeToBaseDir(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	require.NoError(t, os.WriteFile(caPath, []byte(fakeCAPEM), 0o600))

	// Reference the CA by a RELATIVE path — it must resolve under baseDir.
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: file-cluster
    cluster:
      server: https://file.example:6443
      certificate-authority: ca.crt
users:
  - name: u
    user:
      token: t
contexts:
  - name: file-ctx
    context:
      cluster: file-cluster
      user: u
`
	clusters, warnings, err := ParseKubeconfig([]byte(src), dir)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, clusters, 1)
	require.Equal(t, fakeCAPEM, clusters[0].Auth.CABundle, "certificate-authority file must be read relative to baseDir")
}

func TestParseKubeconfigInsecureSkipTLSVerify(t *testing.T) {
	src := `
apiVersion: v1
kind: Config
clusters:
  - name: insecure-cluster
    cluster:
      server: https://insecure.example:6443
      insecure-skip-tls-verify: true
users:
  - name: u
    user:
      token: t
contexts:
  - name: insecure-ctx
    context:
      cluster: insecure-cluster
      user: u
`
	clusters, _, err := ParseKubeconfig([]byte(src), t.TempDir())
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	require.True(t, clusters[0].Auth.K8sInsecureSkipVerify, "insecure-skip-tls-verify: true must set the flag")
	require.Empty(t, clusters[0].Auth.CABundle)
}

func TestDiscoverKubeconfigPaths(t *testing.T) {
	// $KUBECONFIG with multiple entries splits on the OS path-list separator.
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	t.Setenv("KUBECONFIG", a+string(os.PathListSeparator)+b)
	paths := DiscoverKubeconfigPaths()
	require.Equal(t, []string{a, b}, paths)

	// Unset → <home>/.kube/config.
	t.Setenv("KUBECONFIG", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(home, ".kube", "config")}, DiscoverKubeconfigPaths())
}
