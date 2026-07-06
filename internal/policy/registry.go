package policy

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sipaha/outwall/internal/store"
)

// rowExecutor is the subset of *sql.DB / *sql.Tx that insertRule needs, so a single insert helper
// serves both the autocommit Create path and the transactional CreateMany path.
type rowExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

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
	out, err := insertRule(r.store.DB(), in)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateMany persists all rules in a single transaction: either every rule is committed or none is
// (a per-rule validation/insert error rolls the whole batch back). Used by the approval fan-out paths
// so a mid-batch failure can never leave a partial grant.
//
// The store runs on a single shared connection (maxOpenConns=1), so a caller must NOT hold an open
// *sql.Rows on that connection across this call — materialize any prior read (as ForUpstream does)
// before invoking CreateMany, or the write transaction will block on the same connection.
func (r *Registry) CreateMany(ins []Rule) ([]Rule, error) {
	tx, err := r.store.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	out := make([]Rule, 0, len(ins))
	for _, in := range ins {
		ruleOut, ierr := insertRule(tx, in)
		if ierr != nil {
			_ = tx.Rollback()
			return nil, ierr
		}
		out = append(out, ruleOut)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return out, nil
}

// insertRule validates `in`, assigns its ID + CreatedAt, and inserts it via exec (a *sql.DB for the
// autocommit path or a *sql.Tx for a batch). The 18-column INSERT lives here only.
func insertRule(exec rowExecutor, in Rule) (Rule, error) {
	if !ValidOutcome(in.Outcome) {
		return Rule{}, fmt.Errorf("invalid outcome %q", in.Outcome)
	}
	if in.RateLimitPerMin < 0 {
		return Rule{}, fmt.Errorf("rate limit must be >= 0")
	}
	queryJSON, err := marshalJSONMap(in.OpQueryTemplate)
	if err != nil {
		return Rule{}, fmt.Errorf("marshal op_query_template: %w", err)
	}
	bodyJSON, err := marshalJSONMap(in.OpBodyTemplate)
	if err != nil {
		return Rule{}, fmt.Errorf("marshal op_body_template: %w", err)
	}
	policiesJSON, err := marshalValuePolicies(in.OpValuePolicies)
	if err != nil {
		return Rule{}, fmt.Errorf("marshal op_value_policies: %w", err)
	}
	params := in.ProfileParams
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	in.ID = newID()
	in.CreatedAt = time.Now().UTC()
	_, err = exec.Exec(
		`INSERT INTO rules (id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.SubjectAgentID, in.UpstreamID, in.OpMethod, in.OpPathTemplate, queryJSON, bodyJSON, policiesJSON,
		in.Outcome, in.RateLimitPerMin,
		in.Namespace, in.Resource, in.Verb,
		in.Profile, string(params),
		in.BrowseMethods, in.BrowsePath,
		in.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Rule{}, fmt.Errorf("insert rule: %w", err)
	}
	return in, nil
}

// updatePolicies loads a rule's value policies, applies mutate, and persists the result. mutate
// returns false to signal a no-op (skip the write).
func (r *Registry) updatePolicies(ruleID string, mutate func(map[string]ValuePolicy) (bool, error)) error {
	row := r.store.DB().QueryRow(`SELECT op_value_policies FROM rules WHERE id=?`, ruleID)
	var policiesJSON string
	if err := row.Scan(&policiesJSON); err != nil {
		return fmt.Errorf("load rule %s: %w", ruleID, err)
	}
	policies, err := unmarshalValuePolicies(policiesJSON)
	if err != nil {
		return fmt.Errorf("unmarshal op_value_policies: %w", err)
	}
	if policies == nil {
		policies = map[string]ValuePolicy{}
	}
	changed, err := mutate(policies)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	updated, err := marshalValuePolicies(policies)
	if err != nil {
		return fmt.Errorf("marshal op_value_policies: %w", err)
	}
	if _, err := r.store.DB().Exec(`UPDATE rules SET op_value_policies=? WHERE id=?`, updated, ruleID); err != nil {
		return fmt.Errorf("update rule %s: %w", ruleID, err)
	}
	return nil
}

// AddAllowedValue extends a text variable's allowed-set on an existing rule. It is idempotent on
// a value already present. The proxy/approval path calls it when the operator approves a new
// value, so the same operation template's set grows rather than spawning a new rule.
func (r *Registry) AddAllowedValue(ruleID, varName, value string) error {
	return r.updatePolicies(ruleID, func(policies map[string]ValuePolicy) (bool, error) {
		vp, ok := policies[varName]
		if !ok {
			return false, fmt.Errorf("rule %s has no variable %q", ruleID, varName)
		}
		for _, v := range vp.Values {
			if v == value {
				return false, nil // already present — no-op
			}
		}
		vp.Values = append(vp.Values, value)
		policies[varName] = vp
		return true, nil
	})
}

// SetVariableAny flips a text variable's policy to mode "any" (the operator's "trust any value"),
// dropping its now-redundant allowed-set. Idempotent when the variable is already "any". Used by
// the operation-approval resolve path.
func (r *Registry) SetVariableAny(ruleID, varName string) error {
	return r.updatePolicies(ruleID, func(policies map[string]ValuePolicy) (bool, error) {
		vp, ok := policies[varName]
		if !ok {
			return false, fmt.Errorf("rule %s has no variable %q", ruleID, varName)
		}
		if vp.Mode == "any" && vp.Values == nil {
			return false, nil // already any — no-op
		}
		vp.Mode = "any"
		vp.Values = nil
		policies[varName] = vp
		return true, nil
	})
}

// SetVariablePolicy replaces a single variable's value policy on an existing rule (the Operations
// screen's add/remove-value and trust-any toggle, computed client-side and posted whole). It keeps
// the variable's declared Type — the operator can change Mode/Values but not retype a slot — and
// normalises a "set" policy's Values to a deduped, non-nil slice. It rejects an unknown variable so
// a typo can never silently widen the rule.
func (r *Registry) SetVariablePolicy(ruleID, varName string, vp ValuePolicy) error {
	return r.updatePolicies(ruleID, func(policies map[string]ValuePolicy) (bool, error) {
		cur, ok := policies[varName]
		if !ok {
			return false, fmt.Errorf("rule %s has no variable %q", ruleID, varName)
		}
		// Keep the declared type — the operator can change Mode/Values/range but not retype a slot.
		next := ValuePolicy{Type: cur.Type, Mode: vp.Mode}
		switch {
		case next.Mode == "any":
			// any: drop set + bounds.
		case cur.Type == "number":
			next.Mode = "range"
			next.Min, next.Max = vp.Min, vp.Max
		default: // text / enum
			next.Mode = "set"
			next.Values = dedupe(vp.Values)
		}
		policies[varName] = next
		return true, nil
	})
}

// dedupe returns xs with duplicates and empty strings removed, preserving order. A nil/empty input
// yields an empty (non-nil) slice so a "set" policy with no values marshals as [] not null.
func dedupe(xs []string) []string {
	out := make([]string, 0, len(xs))
	seen := map[string]bool{}
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// Delete removes a rule by ID.
func (r *Registry) Delete(id string) error {
	_, err := r.store.DB().Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	return nil
}

func (r *Registry) scanRows(query string, args ...any) (out []*Rule, err error) {
	rows, err := r.store.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	for rows.Next() {
		var (
			rule         Rule
			queryJSON    string
			bodyJSON     string
			policiesJSON string
			profileParam string
			created      string
		)
		if err := rows.Scan(&rule.ID, &rule.SubjectAgentID, &rule.UpstreamID,
			&rule.OpMethod, &rule.OpPathTemplate, &queryJSON, &bodyJSON, &policiesJSON,
			&rule.Outcome, &rule.RateLimitPerMin,
			&rule.Namespace, &rule.Resource, &rule.Verb,
			&rule.Profile, &profileParam, &rule.BrowseMethods, &rule.BrowsePath, &created); err != nil {
			return nil, err
		}
		rule.ProfileParams = json.RawMessage(profileParam)
		if rule.OpQueryTemplate, err = unmarshalJSONMap(queryJSON); err != nil {
			return nil, fmt.Errorf("unmarshal op_query_template: %w", err)
		}
		if rule.OpBodyTemplate, err = unmarshalJSONMap(bodyJSON); err != nil {
			return nil, fmt.Errorf("unmarshal op_body_template: %w", err)
		}
		if rule.OpValuePolicies, err = unmarshalValuePolicies(policiesJSON); err != nil {
			return nil, fmt.Errorf("unmarshal op_value_policies: %w", err)
		}
		rule.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &rule)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const ruleCols = `id, subject_agent_id, upstream_id, op_method, op_path_template, op_query_template, op_body_template, op_value_policies, outcome, rate_limit_per_min, k8s_namespace, k8s_resource, k8s_verb, profile, profile_params, browse_methods, browse_path, created_at`

// List returns all rules ordered by creation time, newest first.
func (r *Registry) List() ([]*Rule, error) {
	return r.scanRows(`SELECT ` + ruleCols + ` FROM rules ORDER BY created_at DESC`)
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
