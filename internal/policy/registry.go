package policy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// Registry persists policy rules.
type Registry struct{ store *store.Store }

// NewRegistry constructs a policy registry.
func NewRegistry(s *store.Store) *Registry { return &Registry{store: s} }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create validates and persists a new rule, assigning an ID and CreatedAt.
func (r *Registry) Create(in Rule) (*Rule, error) {
	if !ValidOutcome(in.Outcome) {
		return nil, fmt.Errorf("invalid outcome %q", in.Outcome)
	}
	if in.RateLimitPerMin < 0 {
		return nil, fmt.Errorf("rate limit must be >= 0")
	}
	// Default the path glob only for http rules; k8s rules match on namespace/resource/verb.
	isK8sRule := in.Namespace != "" || in.Resource != "" || in.Verb != ""
	if in.PathGlob == "" && !isK8sRule {
		in.PathGlob = "/**"
	}
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	_, err := r.store.DB().Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, method, path_glob, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.Method, in.PathGlob, in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert rule: %w", err)
	}
	return &in, nil
}

// Delete removes a rule by ID.
func (r *Registry) Delete(id string) error {
	_, err := r.store.DB().Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	return nil
}

func (r *Registry) scanRows(query string, args ...any) ([]*Rule, error) {
	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()
	var out []*Rule
	for rows.Next() {
		var (
			rule    Rule
			created string
		)
		if err := rows.Scan(&rule.ID, &rule.SubjectAgentID, &rule.UpstreamID, &rule.Method,
			&rule.PathGlob, &rule.Outcome, &rule.RateLimitPerMin,
			&rule.Namespace, &rule.Resource, &rule.Verb, &created); err != nil {
			return nil, err
		}
		rule.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &rule)
	}
	return out, rows.Err()
}

const ruleCols = `id, subject_agent_id, upstream_id, method, path_glob, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, created_at`

// List returns all rules ordered by creation time.
func (r *Registry) List() ([]*Rule, error) {
	return r.scanRows(`SELECT ` + ruleCols + ` FROM rules ORDER BY created_at`)
}

// ForUpstream returns all rules bound to the given upstream.
func (r *Registry) ForUpstream(upstreamID string) ([]*Rule, error) {
	return r.scanRows(`SELECT `+ruleCols+` FROM rules WHERE upstream_id=?`, upstreamID)
}
