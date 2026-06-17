// Package grant is the Plan-1 stand-in for the policy engine: a flat allow-list of
// (agent, upstream) pairs enforcing default-deny. Replaced by internal/policy in Plan 2.
package grant

import (
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// Registry persists allow rows.
type Registry struct {
	store *store.Store
}

// NewRegistry constructs a grant registry.
func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

// Add grants an agent access to an upstream (idempotent).
func (r *Registry) Add(agentID, upstreamID string) error {
	_, err := r.store.DB().Exec(
		`INSERT OR IGNORE INTO grants (agent_id, upstream_id, created_at) VALUES (?, ?, ?)`,
		agentID, upstreamID, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert grant: %w", err)
	}
	return nil
}

// Allowed reports whether an agent may use an upstream.
func (r *Registry) Allowed(agentID, upstreamID string) (bool, error) {
	var n int
	err := r.store.DB().QueryRow(
		`SELECT COUNT(*) FROM grants WHERE agent_id=? AND upstream_id=?`, agentID, upstreamID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("query grant: %w", err)
	}
	return n > 0, nil
}
