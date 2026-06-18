package policy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// Create validates and persists a new rule, assigning an ID and CreatedAt. HTTP operation rules
// marshal OpQueryTemplate/OpValuePolicies to JSON columns; k8s rules leave them empty.
func (r *Registry) Create(in Rule) (*Rule, error) {
	if !ValidOutcome(in.Outcome) {
		return nil, fmt.Errorf("invalid outcome %q", in.Outcome)
	}
	if in.RateLimitPerMin < 0 {
		return nil, fmt.Errorf("rate limit must be >= 0")
	}
	queryJSON, err := marshalJSONMap(in.OpQueryTemplate)
	if err != nil {
		return nil, fmt.Errorf("marshal op_query_template: %w", err)
	}
	policiesJSON, err := marshalValuePolicies(in.OpValuePolicies)
	if err != nil {
		return nil, fmt.Errorf("marshal op_value_policies: %w", err)
	}
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	_, err = r.store.DB().Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.OpMethod, in.OpPathTemplate, queryJSON, policiesJSON,
		in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert rule: %w", err)
	}
	return &in, nil
}

// AddAllowedValue extends a text variable's allowed-set on an existing rule. It is idempotent on
// a value already present. The proxy/approval path calls it when the operator approves a new
// value, so the same operation template's set grows rather than spawning a new rule.
func (r *Registry) AddAllowedValue(ruleID, varName, value string) error {
	row := r.store.DB().QueryRow(`SELECT op_value_policies FROM rules WHERE id=?`, ruleID)
	var policiesJSON string
	if err := row.Scan(&policiesJSON); err != nil {
		return fmt.Errorf("load rule %s: %w", ruleID, err)
	}
	policies, err := unmarshalValuePolicies(policiesJSON)
	if err != nil {
		return fmt.Errorf("unmarshal op_value_policies: %w", err)
	}
	vp, ok := policies[varName]
	if !ok {
		return fmt.Errorf("rule %s has no variable %q", ruleID, varName)
	}
	for _, v := range vp.Values {
		if v == value {
			return nil // already present — no-op
		}
	}
	vp.Values = append(vp.Values, value)
	policies[varName] = vp
	updated, err := marshalValuePolicies(policies)
	if err != nil {
		return fmt.Errorf("marshal op_value_policies: %w", err)
	}
	if _, err := r.store.DB().Exec(`UPDATE rules SET op_value_policies=? WHERE id=?`, updated, ruleID); err != nil {
		return fmt.Errorf("update rule %s: %w", ruleID, err)
	}
	return nil
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
			rule         Rule
			queryJSON    string
			policiesJSON string
			created      string
		)
		if err := rows.Scan(&rule.ID, &rule.SubjectAgentID, &rule.UpstreamID,
			&rule.OpMethod, &rule.OpPathTemplate, &queryJSON, &policiesJSON,
			&rule.Outcome, &rule.RateLimitPerMin,
			&rule.Namespace, &rule.Resource, &rule.Verb, &created); err != nil {
			return nil, err
		}
		if rule.OpQueryTemplate, err = unmarshalJSONMap(queryJSON); err != nil {
			return nil, fmt.Errorf("unmarshal op_query_template: %w", err)
		}
		if rule.OpValuePolicies, err = unmarshalValuePolicies(policiesJSON); err != nil {
			return nil, fmt.Errorf("unmarshal op_value_policies: %w", err)
		}
		rule.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &rule)
	}
	return out, rows.Err()
}

const ruleCols = `id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, created_at`

// List returns all rules ordered by creation time.
func (r *Registry) List() ([]*Rule, error) {
	return r.scanRows(`SELECT ` + ruleCols + ` FROM rules ORDER BY created_at`)
}

// ForUpstream returns all rules bound to the given upstream.
func (r *Registry) ForUpstream(upstreamID string) ([]*Rule, error) {
	return r.scanRows(`SELECT `+ruleCols+` FROM rules WHERE upstream_id=?`, upstreamID)
}

// marshalJSONMap renders a string map as a JSON object, normalizing nil to "{}".
func marshalJSONMap(m map[string]string) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalJSONMap(s string) (map[string]string, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func marshalValuePolicies(m map[string]ValuePolicy) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalValuePolicies(s string) (map[string]ValuePolicy, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string]ValuePolicy
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}
