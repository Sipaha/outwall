// Package access is the registry of access-request intents: the log of which agent asked for
// which upstream, with what stated purpose, and the operator's decision. Access itself remains
// rule-derived (see internal/policy); these records are the operator's queue/audit of intent.
package access

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	// StatusRevoked marks a previously granted request whose access was later withdrawn by the
	// operator (MarkRevoked) — distinct from StatusDenied (never granted in the first place).
	StatusRevoked = "revoked"
)

// BindingEdit records one preset slot the operator changed when approving a request: the value the
// agent asked for versus the value that was actually granted. Surfaced to the agent so it learns the
// operator narrowed its request (e.g. workspace "*" → "ECOSENT").
type BindingEdit struct {
	Slot      string `json:"slot"`
	Requested string `json:"requested"`
	Granted   string `json:"granted"`
}

// DiffBindings returns, sorted by slot, the entries whose granted value differs from the requested
// value (the union of both maps' keys; a slot absent on one side compares against ""). An empty
// result means the operator approved the request unchanged.
func DiffBindings(requested, granted map[string]string) []BindingEdit {
	keys := map[string]struct{}{}
	for k := range requested {
		keys[k] = struct{}{}
	}
	for k := range granted {
		keys[k] = struct{}{}
	}
	var edits []BindingEdit
	for k := range keys {
		if requested[k] != granted[k] {
			edits = append(edits, BindingEdit{Slot: k, Requested: requested[k], Granted: granted[k]})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].Slot < edits[j].Slot })
	return edits
}

// Request is a logged access-request intent.
type Request struct {
	ID         string
	AgentID    string
	UpstreamID string
	Purpose    string
	Status     string
	Reason     string // operator's deny reason (when Status == denied), surfaced to the agent
	// Edits are the preset slots the operator narrowed when granting (empty when unchanged or for
	// non-preset requests), surfaced to the agent alongside the grant.
	Edits      []BindingEdit
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

const reqCols = `id, agent_id, upstream_id, purpose, status, reason, edits, created_at, resolved_at`

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
			edits   string
		)
		if err := rows.Scan(&req.ID, &req.AgentID, &req.UpstreamID, &req.Purpose,
			&req.Status, &req.Reason, &edits, &created, &req.ResolvedAt); err != nil {
			return nil, err
		}
		if edits != "" {
			if err := json.Unmarshal([]byte(edits), &req.Edits); err != nil {
				return nil, fmt.Errorf("decode access-request edits: %w", err)
			}
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

// DenyLatest marks the most recent PENDING request for (agentID, upstreamID) as denied with the
// given reason, stamping resolved_at. It reports whether a row was updated (false when the agent has
// no pending request for that upstream). Used by the approval-resolve path so an MCP agent learns
// WHY when it polls get_access.
func (r *Registry) DenyLatest(agentID, upstreamID, reason string) (bool, error) {
	res, err := r.store.DB().Exec(
		`UPDATE access_requests SET status=?, reason=?, resolved_at=?
		 WHERE id = (SELECT id FROM access_requests
		             WHERE agent_id=? AND upstream_id=? AND status=?
		             ORDER BY created_at DESC LIMIT 1)`,
		StatusDenied, reason, time.Now().UTC().Format(time.RFC3339Nano),
		agentID, upstreamID, StatusPending,
	)
	if err != nil {
		return false, fmt.Errorf("deny latest access request: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// GrantLatest marks the most recent PENDING request for (agentID, upstreamID) as granted, stamping
// resolved_at and recording any operator slot edits. It reports whether a row was updated. Used by
// the approval-resolve path so the access-request history stays in sync with a card Approve (see
// ADR-0025) and the agent learns of an operator narrowing (see ADR-0044).
func (r *Registry) GrantLatest(agentID, upstreamID string, edits []BindingEdit) (bool, error) {
	editsJSON := ""
	if len(edits) > 0 {
		b, err := json.Marshal(edits)
		if err != nil {
			return false, fmt.Errorf("encode access-request edits: %w", err)
		}
		editsJSON = string(b)
	}
	res, err := r.store.DB().Exec(
		`UPDATE access_requests SET status=?, resolved_at=?, edits=?
		 WHERE id = (SELECT id FROM access_requests
		             WHERE agent_id=? AND upstream_id=? AND status=?
		             ORDER BY created_at DESC LIMIT 1)`,
		StatusGranted, time.Now().UTC().Format(time.RFC3339Nano), editsJSON,
		agentID, upstreamID, StatusPending,
	)
	if err != nil {
		return false, fmt.Errorf("grant latest access request: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Latest returns the most recent access request for (agentID, upstreamID), or (nil,false) if none.
// get_access consults it so a just-denied request surfaces its status + reason to the agent.
func (r *Registry) Latest(agentID, upstreamID string) (*Request, bool, error) {
	reqs, err := r.scanRows(
		`SELECT `+reqCols+` FROM access_requests WHERE agent_id=? AND upstream_id=? ORDER BY created_at DESC LIMIT 1`,
		agentID, upstreamID,
	)
	if err != nil {
		return nil, false, err
	}
	if len(reqs) == 0 {
		return nil, false, nil
	}
	return reqs[0], true, nil
}

// GetByID returns the access request with the given ID, or ErrNotFound.
func (r *Registry) GetByID(id string) (*Request, error) {
	reqs, err := r.scanRows(`SELECT `+reqCols+` FROM access_requests WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, ErrNotFound
	}
	return reqs[0], nil
}

// MarkRevoked marks an access request "revoked" and stamps resolved_at. Used by the operator's
// revoke action (which also removes the underlying policy rules) to keep the request's history
// entry consistent with the fact that access was withdrawn, distinct from "denied" (never granted)
// or "dismissed" (an operator no-op on a pending card).
func (r *Registry) MarkRevoked(id string) error {
	res, err := r.store.DB().Exec(
		`UPDATE access_requests SET status=?, resolved_at=? WHERE id=?`,
		StatusRevoked, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("mark access request revoked: %w", err)
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

// MarkRevokedBySubjectUpstream marks every currently-granted request for (agentID, upstreamID)
// "revoked" and stamps resolved_at. Used by the operator's grant revoke (which also removes the
// underlying policy rules). Returns the number of requests marked. Pending/denied rows are left
// untouched.
func (r *Registry) MarkRevokedBySubjectUpstream(agentID, upstreamID string) (int64, error) {
	res, err := r.store.DB().Exec(
		`UPDATE access_requests SET status=?, resolved_at=? WHERE agent_id=? AND upstream_id=? AND status=?`,
		StatusRevoked, time.Now().UTC().Format(time.RFC3339Nano), agentID, upstreamID, StatusGranted,
	)
	if err != nil {
		return 0, fmt.Errorf("mark revoked by subject+upstream: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
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
