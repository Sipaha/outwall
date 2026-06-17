// Package access is the registry of access-request intents: the log of which agent asked for
// which upstream, with what stated purpose, and the operator's decision. Access itself remains
// rule-derived (see internal/policy); these records are the operator's queue/audit of intent.
package access

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// ErrNotFound is returned when an access request does not exist.
var ErrNotFound = errors.New("access request not found")

// Statuses an access request can hold.
const (
	StatusPending   = "pending"
	StatusGranted   = "granted"
	StatusDenied    = "denied"
	StatusDismissed = "dismissed"
)

// Request is a logged access-request intent.
type Request struct {
	ID         string
	AgentID    string
	UpstreamID string
	Purpose    string
	Status     string
	CreatedAt  time.Time
	ResolvedAt string
}

// Registry persists access-request intents.
type Registry struct{ store *store.Store }

// NewRegistry constructs an access-request registry.
func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func validResolveStatus(s string) bool {
	return s == StatusGranted || s == StatusDenied || s == StatusDismissed
}

// Create logs a new access-request intent with status "pending".
func (r *Registry) Create(agentID, upstreamID, purpose string) (*Request, error) {
	req := &Request{
		ID: newID(), AgentID: agentID, UpstreamID: upstreamID, Purpose: purpose,
		Status: StatusPending, CreatedAt: time.Now().UTC(),
	}
	_, err := r.store.DB().Exec(
		`INSERT INTO access_requests (id, agent_id, upstream_id, purpose, status, created_at, resolved_at)
		 VALUES (?, ?, ?, ?, ?, ?, '')`,
		req.ID, req.AgentID, req.UpstreamID, req.Purpose, req.Status,
		req.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert access request: %w", err)
	}
	return req, nil
}

const reqCols = `id, agent_id, upstream_id, purpose, status, created_at, resolved_at`

func (r *Registry) scanRows(query string, args ...any) ([]*Request, error) {
	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query access requests: %w", err)
	}
	defer rows.Close()
	var out []*Request
	for rows.Next() {
		var (
			req     Request
			created string
		)
		if err := rows.Scan(&req.ID, &req.AgentID, &req.UpstreamID, &req.Purpose,
			&req.Status, &created, &req.ResolvedAt); err != nil {
			return nil, err
		}
		req.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &req)
	}
	return out, rows.Err()
}

// List returns all access requests, newest first.
func (r *Registry) List() ([]*Request, error) {
	return r.scanRows(`SELECT ` + reqCols + ` FROM access_requests ORDER BY created_at DESC`)
}

// Pending returns access requests still awaiting an operator decision, newest first.
func (r *Registry) Pending() ([]*Request, error) {
	return r.scanRows(
		`SELECT `+reqCols+` FROM access_requests WHERE status=? ORDER BY created_at DESC`,
		StatusPending,
	)
}

// Resolve records the operator's decision (granted/denied/dismissed) and stamps resolved_at.
func (r *Registry) Resolve(id, status string) error {
	if !validResolveStatus(status) {
		return fmt.Errorf("invalid status %q", status)
	}
	res, err := r.store.DB().Exec(
		`UPDATE access_requests SET status=?, resolved_at=? WHERE id=?`,
		status, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("update access request: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
