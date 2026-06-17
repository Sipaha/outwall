// Package audit records every data-plane request/response in SQLite: a fast-to-list
// journal (audit_log) plus a separate body store (audit_bodies). Injected and agent
// credentials are masked; bodies are captured up to a cap via a streaming tee (see
// capture.go). Keep-all by default with manual prune.
package audit

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// ErrNotFound is returned when an audit entry does not exist.
var ErrNotFound = errors.New("audit entry not found")

// Entry is one row of the request journal (no bodies).
type Entry struct {
	ID         string
	TS         time.Time
	AgentID    string
	AgentName  string
	UpstreamID string

	UpstreamName string
	Method       string
	Path         string
	Query        string
	StatusCode   int
	DurationMs   int
	ReqBytes     int64
	RespBytes    int64
	Decision     string
	RuleID       string
	Error        string
	Headers      map[string]string
}

// Body is a captured request or response body (Kind: "request" | "response").
type Body struct {
	Kind        string
	ContentType string
	Size        int64
	Sha256      string
	Truncated   bool
	Stored      []byte
}

// Body kinds.
const (
	KindRequest  = "request"
	KindResponse = "response"
)

// Recorder persists audit entries and their bodies.
type Recorder struct{ store *store.Store }

// NewRecorder constructs an audit recorder over the given store.
func NewRecorder(s *store.Store) *Recorder { return &Recorder{store: s} }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Record inserts the log row and each body row. Assigns e.ID if empty.
func (r *Recorder) Record(e Entry, bodies ...Body) error {
	if e.ID == "" {
		e.ID = newID()
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	headersJSON := "{}"
	if len(e.Headers) > 0 {
		b, err := json.Marshal(e.Headers)
		if err != nil {
			return fmt.Errorf("marshal headers: %w", err)
		}
		headersJSON = string(b)
	}
	_, err := r.store.DB().Exec(
		`INSERT INTO audit_log
			(id, ts, agent_id, agent_name, upstream_id, upstream_name, method, path, query,
			 status_code, duration_ms, req_bytes, resp_bytes, decision, rule_id, headers_json, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TS.Format(time.RFC3339Nano), e.AgentID, e.AgentName, e.UpstreamID, e.UpstreamName,
		e.Method, e.Path, e.Query, e.StatusCode, e.DurationMs, e.ReqBytes, e.RespBytes,
		e.Decision, e.RuleID, headersJSON, e.Error,
	)
	if err != nil {
		return fmt.Errorf("insert audit_log: %w", err)
	}
	for _, b := range bodies {
		var truncated int
		if b.Truncated {
			truncated = 1
		}
		if _, err := r.store.DB().Exec(
			`INSERT INTO audit_bodies (log_id, kind, content_type, size, sha256, truncated, stored)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.ID, b.Kind, b.ContentType, b.Size, b.Sha256, truncated, b.Stored,
		); err != nil {
			return fmt.Errorf("insert audit_bodies: %w", err)
		}
	}
	return nil
}

const entryCols = `id, ts, agent_id, agent_name, upstream_id, upstream_name, method, path, query,
	status_code, duration_ms, req_bytes, resp_bytes, decision, rule_id, headers_json, error`

func scanEntry(sc interface{ Scan(...any) error }) (Entry, error) {
	var (
		e           Entry
		ts          string
		headersJSON string
	)
	if err := sc.Scan(&e.ID, &ts, &e.AgentID, &e.AgentName, &e.UpstreamID, &e.UpstreamName,
		&e.Method, &e.Path, &e.Query, &e.StatusCode, &e.DurationMs, &e.ReqBytes, &e.RespBytes,
		&e.Decision, &e.RuleID, &headersJSON, &e.Error); err != nil {
		return Entry{}, err
	}
	e.TS, _ = time.Parse(time.RFC3339Nano, ts)
	if headersJSON != "" {
		_ = json.Unmarshal([]byte(headersJSON), &e.Headers)
	}
	return e, nil
}

// List returns the newest entries first (no bodies), capped at limit.
func (r *Recorder) List(limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.store.DB().Query(
		`SELECT `+entryCols+` FROM audit_log ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit_log: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns the entry and its bodies; ErrNotFound if absent.
func (r *Recorder) Get(id string) (Entry, []Body, error) {
	row := r.store.DB().QueryRow(`SELECT `+entryCols+` FROM audit_log WHERE id=?`, id)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, nil, ErrNotFound
	}
	if err != nil {
		return Entry{}, nil, fmt.Errorf("query audit_log: %w", err)
	}
	rows, err := r.store.DB().Query(
		`SELECT kind, content_type, size, sha256, truncated, stored
		 FROM audit_bodies WHERE log_id=? ORDER BY kind`, id)
	if err != nil {
		return Entry{}, nil, fmt.Errorf("query audit_bodies: %w", err)
	}
	defer rows.Close()
	var bodies []Body
	for rows.Next() {
		var (
			b    Body
			trun int
		)
		if err := rows.Scan(&b.Kind, &b.ContentType, &b.Size, &b.Sha256, &trun, &b.Stored); err != nil {
			return Entry{}, nil, err
		}
		b.Truncated = trun != 0
		bodies = append(bodies, b)
	}
	if err := rows.Err(); err != nil {
		return Entry{}, nil, err
	}
	return e, bodies, nil
}

// Prune deletes entries with ts < olderThan (and cascades their bodies); returns count.
func (r *Recorder) Prune(olderThan time.Time) (int64, error) {
	cutoff := olderThan.UTC().Format(time.RFC3339Nano)
	if _, err := r.store.DB().Exec(
		`DELETE FROM audit_bodies WHERE log_id IN (SELECT id FROM audit_log WHERE ts < ?)`,
		cutoff,
	); err != nil {
		return 0, fmt.Errorf("delete audit_bodies: %w", err)
	}
	res, err := r.store.DB().Exec(`DELETE FROM audit_log WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete audit_log: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
