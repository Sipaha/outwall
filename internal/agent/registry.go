// Package agent is the registry of agents that connect through outwall.
package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// ErrUnknownToken is returned when a token matches no agent.
var ErrUnknownToken = errors.New("unknown agent token")

// ErrNotFound is returned when no agent matches the given ID.
var ErrNotFound = errors.New("agent not found")

// StatusNew is the default status of a freshly registered agent (default-deny).
const StatusNew = "new"

// Agent is a registered consumer of the gateway.
type Agent struct {
	ID        string
	Name      string
	Status    string
	CreatedAt time.Time
}

// Registry persists agents and their token hashes.
type Registry struct {
	store *store.Store
}

// NewRegistry constructs an agent registry.
func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Register creates a new agent and returns its bearer token (shown once).
func (r *Registry) Register(name string) (*Agent, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("read token: %w", err)
	}
	token := "owa_" + base64.RawURLEncoding.EncodeToString(raw)
	a := &Agent{ID: newID(), Name: name, Status: StatusNew, CreatedAt: time.Now().UTC()}
	_, err := r.store.DB().Exec(
		`INSERT INTO agents (id, name, token_sha256, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.Name, hashToken(token), a.Status, a.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, "", fmt.Errorf("insert agent: %w", err)
	}
	return a, token, nil
}

// Authenticate resolves an agent by its bearer token.
func (r *Registry) Authenticate(token string) (*Agent, error) {
	var (
		a       Agent
		created string
	)
	err := r.store.DB().QueryRow(
		`SELECT id, name, status, created_at FROM agents WHERE token_sha256=?`, hashToken(token),
	).Scan(&a.ID, &a.Name, &a.Status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownToken
	}
	if err != nil {
		return nil, fmt.Errorf("query agent: %w", err)
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &a, nil
}

// GetByID resolves an agent by its ID.
func (r *Registry) GetByID(id string) (*Agent, error) {
	var (
		a       Agent
		created string
	)
	err := r.store.DB().QueryRow(
		`SELECT id, name, status, created_at FROM agents WHERE id=?`, id,
	).Scan(&a.ID, &a.Name, &a.Status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query agent: %w", err)
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &a, nil
}

// List returns all agents, newest first.
func (r *Registry) List() ([]*Agent, error) {
	rows, err := r.store.DB().Query(
		`SELECT id, name, status, created_at FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		var (
			a       Agent
			created string
		)
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &created); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &a)
	}
	return out, rows.Err()
}
