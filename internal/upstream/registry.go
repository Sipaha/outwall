// Package upstream is the registry of named external APIs and their (encrypted) auth config.
package upstream

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/secret"
	"github.com/Sipaha/outwall/internal/store"
)

// ErrNotFound is returned when an upstream does not exist.
var ErrNotFound = errors.New("upstream not found")

// AuthConfig is the (encrypted-at-rest) credential material for an upstream.
type AuthConfig struct {
	Type     string `json:"type"` // none | static | basic | oidc-client-credentials | mtls | sigv4 | hmac
	Header   string `json:"header,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// OIDC client-credentials:
	TokenURL     string `json:"token_url,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	Scope        string `json:"scope,omitempty"`

	// AWS Signature V4 (type "sigv4"). The request is signed with these static credentials.
	AWSAccessKeyID     string `json:"aws_access_key_id,omitempty"`
	AWSSecretAccessKey string `json:"aws_secret_access_key,omitempty"`
	AWSRegion          string `json:"aws_region,omitempty"`
	AWSService         string `json:"aws_service,omitempty"`

	// HMAC request signature (type "hmac"). See ADR-0019 for the canonical-string scheme.
	HMACSecret string `json:"hmac_secret,omitempty"`
	HMACHeader string `json:"hmac_header,omitempty"` // header to carry the signature, e.g. X-Signature
	HMACAlgo   string `json:"hmac_algo,omitempty"`   // sha256 (default) | sha512

	// mTLS (type "mtls") reuses ClientCert/ClientKey (+ optional CABundle) below for an http
	// upstream — the client certificate is presented to the upstream over TLS.

	// Kubernetes cluster connection (when the owning upstream Kind=="k8s"):
	CABundle    string            `json:"ca_bundle,omitempty"`    // PEM, trusts the API server
	K8sAuth     string            `json:"k8s_auth,omitempty"`     // token | client-cert | exec
	ClientCert  string            `json:"client_cert,omitempty"`  // PEM (client-cert auth)
	ClientKey   string            `json:"client_key,omitempty"`   // PEM
	ExecCommand string            `json:"exec_command,omitempty"` // exec auth: binary
	ExecArgs    []string          `json:"exec_args,omitempty"`
	ExecEnv     map[string]string `json:"exec_env,omitempty"`
	// K8sInsecureSkipVerify disables TLS verification of the API server. SECURITY: it is set
	// ONLY by an explicit insecure-skip-tls-verify:true in the operator's own kubeconfig (we
	// mirror the trust decision they already made, like kubectl). Never a default, never
	// agent-settable, never used to paper over a CA error. When a CA bundle is also present the
	// CA wins and this stays false.
	K8sInsecureSkipVerify bool `json:"k8s_insecure_skip_verify,omitempty"`
}

// KindHTTP and KindK8s are the upstream kinds.
const (
	KindHTTP = "http"
	KindK8s  = "k8s"
)

// Upstream is a named external API.
type Upstream struct {
	ID        string
	Name      string
	BaseURL   string
	Kind      string // "http" (default) | "k8s"
	AuthType  string
	Auth      AuthConfig
	CreatedAt time.Time
}

// Registry persists upstreams.
type Registry struct {
	store *store.Store
	vault *secret.Vault
}

// NewRegistry constructs an upstream registry.
func NewRegistry(s *store.Store, v *secret.Vault) *Registry {
	return &Registry{store: s, vault: v}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create encrypts the auth config and stores a new http upstream.
func (r *Registry) Create(name, baseURL string, auth AuthConfig) (*Upstream, error) {
	return r.CreateKind(name, baseURL, KindHTTP, auth)
}

// CreateKind encrypts the auth config and stores a new upstream of the given kind
// ("http" or "k8s"). An empty kind defaults to "http".
func (r *Registry) CreateKind(name, baseURL, kind string, auth AuthConfig) (*Upstream, error) {
	if kind == "" {
		kind = KindHTTP
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		return nil, fmt.Errorf("marshal auth: %w", err)
	}
	enc, err := r.vault.Encrypt(raw)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth: %w", err)
	}
	up := &Upstream{
		ID: newID(), Name: name, BaseURL: baseURL, Kind: kind, AuthType: auth.Type,
		Auth: auth, CreatedAt: time.Now().UTC(),
	}
	_, err = r.store.DB().Exec(
		`INSERT INTO upstreams (id, name, base_url, kind, auth_type, auth_config, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		up.ID, up.Name, up.BaseURL, up.Kind, up.AuthType, enc, up.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert upstream: %w", err)
	}
	return up, nil
}

// GetOrCreateByHost returns the http upstream for the given host, creating a credential-less one
// (name = host, BaseURL = "https://<host>", auth type "none") if it does not yet exist. The
// boolean reports whether it was created. Used by the MCP host-access path: a host is registered
// lazily the first time an agent requests it; the operator attaches the credential later via
// SetAuth at host-approval time. Idempotent — a second call for the same host returns the existing
// upstream with created=false.
func (r *Registry) GetOrCreateByHost(host string) (*Upstream, bool, error) {
	up, err := r.GetByName(host)
	switch {
	case err == nil:
		return up, false, nil
	case errors.Is(err, ErrNotFound):
		// fall through to create
	default:
		return nil, false, err
	}
	created, err := r.Create(host, "https://"+host, AuthConfig{Type: "none"})
	if err != nil {
		return nil, false, err
	}
	return created, true, nil
}

// SetAuth replaces the (encrypted) auth config of an existing upstream by ID and updates its
// stored auth_type. Used by the host-approval resolve path to attach the operator-entered
// credential to a lazily-created host. The token is encrypted via the vault, so it is masked at
// rest exactly like Create's.
func (r *Registry) SetAuth(id string, auth AuthConfig) error {
	raw, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}
	enc, err := r.vault.Encrypt(raw)
	if err != nil {
		return fmt.Errorf("encrypt auth: %w", err)
	}
	res, err := r.store.DB().Exec(
		`UPDATE upstreams SET auth_type=?, auth_config=? WHERE id=?`, auth.Type, enc, id)
	if err != nil {
		return fmt.Errorf("update upstream auth: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Registry) scan(row interface{ Scan(...any) error }) (*Upstream, error) {
	var (
		up      Upstream
		enc     []byte
		created string
	)
	if err := row.Scan(&up.ID, &up.Name, &up.BaseURL, &up.Kind, &up.AuthType, &enc, &created); err != nil {
		return nil, err
	}
	up.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	raw, err := r.vault.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt auth: %w", err)
	}
	if err := json.Unmarshal(raw, &up.Auth); err != nil {
		return nil, fmt.Errorf("unmarshal auth: %w", err)
	}
	return &up, nil
}

// DeleteByName removes the upstream with the given name.
func (r *Registry) DeleteByName(name string) error {
	res, err := r.store.DB().Exec(`DELETE FROM upstreams WHERE name=?`, name)
	if err != nil {
		return fmt.Errorf("delete upstream: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetByName returns the upstream with the given name.
func (r *Registry) GetByName(name string) (*Upstream, error) {
	row := r.store.DB().QueryRow(
		`SELECT id, name, base_url, kind, auth_type, auth_config, created_at FROM upstreams WHERE name=?`, name)
	up, err := r.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return up, nil
}

// List returns all upstreams (vault must be unlocked).
func (r *Registry) List() ([]*Upstream, error) {
	rows, err := r.store.DB().Query(
		`SELECT id, name, base_url, kind, auth_type, auth_config, created_at FROM upstreams ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query upstreams: %w", err)
	}
	defer rows.Close()
	var out []*Upstream
	for rows.Next() {
		up, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, up)
	}
	return out, rows.Err()
}
