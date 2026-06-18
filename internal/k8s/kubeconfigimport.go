package k8s

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/Sipaha/outwall/internal/upstream"
)

// ParsedCluster is one kubeconfig context flattened into an outwall cluster target.
type ParsedCluster struct {
	Name   string              // the context name (becomes the outwall upstream name)
	Server string              // cluster.server (the API URL → upstream BaseURL)
	Auth   upstream.AuthConfig // K8sAuth=token|client-cert|exec (+ CABundle / insecure)
}

// DiscoverKubeconfigPaths returns every kubeconfig file to read: the $KUBECONFIG entries (when
// set) PLUS every regular file directly under <home>/.kube/, de-duplicated. Unlike kubectl —
// which uses ONLY $KUBECONFIG when it is set — outwall additionally scans ~/.kube so the
// operator's clusters in sibling files (the way Lens aggregates them) are all imported; this is a
// deliberate divergence (see ADR-0012). The Importer skips a discovered file that is not a
// kubeconfig (and missing files), so the extra paths are harmless.
func DiscoverKubeconfigPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// No home → fall back to whatever $KUBECONFIG names (possibly none).
		return discoverKubeconfigPathsIn("", os.Getenv("KUBECONFIG"))
	}
	return discoverKubeconfigPathsIn(filepath.Join(home, ".kube"), os.Getenv("KUBECONFIG"))
}

// discoverKubeconfigPathsIn returns the $KUBECONFIG entries (from kubeconfigEnv, split on the OS
// path-list separator; may be empty) followed by every regular file directly under kubeDir
// (subdirectories like cache/ and http-cache/ are skipped), de-duplicated, in a stable order
// (env entries first, then the dir files sorted by name). A missing/unreadable kubeDir yields
// just the env entries. Exposed (lowercase) for tests; DiscoverKubeconfigPaths supplies the real
// <home>/.kube and env.
func discoverKubeconfigPathsIn(kubeDir, kubeconfigEnv string) []string {
	var paths []string
	seen := map[string]struct{}{}
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}

	for _, p := range filepath.SplitList(kubeconfigEnv) {
		add(p)
	}

	if kubeDir != "" {
		if entries, err := os.ReadDir(kubeDir); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue // skip cache/, http-cache/, …
				}
				names = append(names, e.Name())
			}
			sort.Strings(names)
			for _, n := range names {
				add(filepath.Join(kubeDir, n))
			}
		}
	}
	return paths
}

// --- minimal kubeconfig schema (only the fields we consume) ---

type kubeconfigDoc struct {
	Clusters []namedCluster `yaml:"clusters"`
	Users    []namedUser    `yaml:"users"`
	Contexts []namedContext `yaml:"contexts"`
}

type namedCluster struct {
	Name    string         `yaml:"name"`
	Cluster clusterDetails `yaml:"cluster"`
}

type clusterDetails struct {
	Server                   string `yaml:"server"`
	CertificateAuthority     string `yaml:"certificate-authority"`
	CertificateAuthorityData string `yaml:"certificate-authority-data"`
	InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
}

type namedUser struct {
	Name string      `yaml:"name"`
	User userDetails `yaml:"user"`
}

type userDetails struct {
	Token                 string    `yaml:"token"`
	TokenFile             string    `yaml:"tokenFile"`
	ClientCertificate     string    `yaml:"client-certificate"`
	ClientCertificateData string    `yaml:"client-certificate-data"`
	ClientKey             string    `yaml:"client-key"`
	ClientKeyData         string    `yaml:"client-key-data"`
	Exec                  *execAuth `yaml:"exec"`
}

type execAuth struct {
	Command string        `yaml:"command"`
	Args    []string      `yaml:"args"`
	Env     []execEnvPair `yaml:"env"`
}

type execEnvPair struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type namedContext struct {
	Name    string         `yaml:"name"`
	Context contextDetails `yaml:"context"`
}

type contextDetails struct {
	Cluster string `yaml:"cluster"`
	User    string `yaml:"user"`
}

// ParseKubeconfig flattens every context in one kubeconfig file's bytes into ParsedClusters,
// resolving file-path refs (certificate-authority, client-certificate, client-key, tokenFile)
// relative to baseDir and decoding the base64 *-data fields. A context that cannot be turned
// into a usable cluster target is skipped with a reason in warnings — never a whole-file error
// (a single malformed context must not lose the rest). A YAML parse failure is a real error.
func ParseKubeconfig(data []byte, baseDir string) (clusters []ParsedCluster, warnings []string, err error) {
	var doc kubeconfigDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse kubeconfig yaml: %w", err)
	}

	clustersByName := map[string]clusterDetails{}
	for _, c := range doc.Clusters {
		clustersByName[c.Name] = c.Cluster
	}
	usersByName := map[string]userDetails{}
	for _, u := range doc.Users {
		usersByName[u.Name] = u.User
	}

	for _, ctx := range doc.Contexts {
		cl, ok := clustersByName[ctx.Context.Cluster]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("context %q: unknown cluster %q", ctx.Name, ctx.Context.Cluster))
			continue
		}
		usr, ok := usersByName[ctx.Context.User]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("context %q: unknown user %q", ctx.Name, ctx.Context.User))
			continue
		}
		if cl.Server == "" {
			warnings = append(warnings, fmt.Sprintf("context %q: cluster has no server", ctx.Name))
			continue
		}

		auth, warn := buildAuthConfig(ctx.Name, cl, usr, baseDir)
		if warn != "" {
			warnings = append(warnings, warn)
			continue
		}
		clusters = append(clusters, ParsedCluster{Name: ctx.Name, Server: cl.Server, Auth: auth})
	}
	return clusters, warnings, nil
}

// buildAuthConfig maps one (cluster,user) pair into an upstream.AuthConfig. The returned warn
// string is non-empty when the context cannot be supported (caller skips it).
func buildAuthConfig(ctxName string, cl clusterDetails, usr userDetails, baseDir string) (upstream.AuthConfig, string) {
	auth := upstream.AuthConfig{Type: "none"}

	// CA: prefer the inline base64 data, else the file path (relative to baseDir).
	ca, warn := resolveDataOrFile("certificate-authority", ctxName, cl.CertificateAuthorityData, cl.CertificateAuthority, baseDir)
	if warn != "" {
		return upstream.AuthConfig{}, warn
	}
	auth.CABundle = ca

	// SECURITY: insecure is operator-explicit only, and the CA wins when both are present.
	if cl.InsecureSkipTLSVerify && auth.CABundle == "" {
		auth.K8sInsecureSkipVerify = true
	}

	switch {
	case usr.Exec != nil && usr.Exec.Command != "":
		auth.K8sAuth = "exec"
		auth.ExecCommand = usr.Exec.Command
		auth.ExecArgs = usr.Exec.Args
		if len(usr.Exec.Env) > 0 {
			env := make(map[string]string, len(usr.Exec.Env))
			for _, e := range usr.Exec.Env {
				env[e.Name] = e.Value
			}
			auth.ExecEnv = env
		}
	case usr.ClientCertificateData != "" || usr.ClientCertificate != "":
		cert, w := resolveDataOrFile("client-certificate", ctxName, usr.ClientCertificateData, usr.ClientCertificate, baseDir)
		if w != "" {
			return upstream.AuthConfig{}, w
		}
		key, w := resolveDataOrFile("client-key", ctxName, usr.ClientKeyData, usr.ClientKey, baseDir)
		if w != "" {
			return upstream.AuthConfig{}, w
		}
		if cert == "" || key == "" {
			return upstream.AuthConfig{}, fmt.Sprintf("context %q: client-cert auth needs both client-certificate and client-key", ctxName)
		}
		auth.K8sAuth = "client-cert"
		auth.ClientCert = cert
		auth.ClientKey = key
	case usr.Token != "":
		auth.K8sAuth = "token"
		auth.Token = usr.Token
	case usr.TokenFile != "":
		tok, w := readFileRel(usr.TokenFile, baseDir)
		if w != "" {
			return upstream.AuthConfig{}, fmt.Sprintf("context %q: %s", ctxName, w)
		}
		auth.K8sAuth = "token"
		auth.Token = tok
	default:
		return upstream.AuthConfig{}, fmt.Sprintf("context %q: no supported auth (token/client-cert/exec)", ctxName)
	}

	return auth, ""
}

// resolveDataOrFile returns the PEM/text for a kubeconfig field that may be supplied inline as
// base64 (*-data) or as a file path (relative to baseDir). It returns a non-empty warn string
// describing the field on failure. An absent field yields ("", "").
func resolveDataOrFile(field, ctxName, dataB64, path, baseDir string) (string, string) {
	if dataB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(dataB64)
		if err != nil {
			return "", fmt.Sprintf("context %q: decode %s-data: %v", ctxName, field, err)
		}
		return string(raw), ""
	}
	if path != "" {
		content, warn := readFileRel(path, baseDir)
		if warn != "" {
			return "", fmt.Sprintf("context %q: %s %s", ctxName, field, warn)
		}
		return content, ""
	}
	return "", ""
}

// readFileRel reads a file whose path may be relative to baseDir.
func readFileRel(path, baseDir string) (string, string) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Sprintf("read %s: %v", path, err)
	}
	return string(b), ""
}
