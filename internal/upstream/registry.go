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
// OIDC fields are added in Plan 2.
type AuthConfig struct {
	Type     string `json:"type"` // none | static | basic
	Header   string `json:"header,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Upstream is a named external API.
type Upstream struct {
	ID        string
	Name      string
	BaseURL   string
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

// Create encrypts the auth config and stores a new upstream.
func (r *Registry) Create(name, baseURL string, auth AuthConfig) (*Upstream, error) {
	raw, err := json.Marshal(auth)
	if err != nil {
		return nil, fmt.Errorf("marshal auth: %w", err)
	}
	enc, err := r.vault.Encrypt(raw)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth: %w", err)
	}
	up := &Upstream{
		ID: newID(), Name: name, BaseURL: baseURL, AuthType: auth.Type,
		Auth: auth, CreatedAt: time.Now().UTC(),
	}
	_, err = r.store.DB().Exec(
		`INSERT INTO upstreams (id, name, base_url, auth_type, auth_config, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		up.ID, up.Name, up.BaseURL, up.AuthType, enc, up.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert upstream: %w", err)
	}
	return up, nil
}

func (r *Registry) scan(row interface{ Scan(...any) error }) (*Upstream, error) {
	var (
		up      Upstream
		enc     []byte
		created string
	)
	if err := row.Scan(&up.ID, &up.Name, &up.BaseURL, &up.AuthType, &enc, &created); err != nil {
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

// GetByName returns the upstream with the given name.
func (r *Registry) GetByName(name string) (*Upstream, error) {
	row := r.store.DB().QueryRow(
		`SELECT id, name, base_url, auth_type, auth_config, created_at FROM upstreams WHERE name=?`, name)
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
		`SELECT id, name, base_url, auth_type, auth_config, created_at FROM upstreams ORDER BY name`)
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
