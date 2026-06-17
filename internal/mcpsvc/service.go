// Package mcpsvc is the SDK-free domain service behind the four MCP control-plane tools.
//
// It resolves a host/upstream, derives an agent's per-upstream status from the policy rules,
// and builds the tool responses. It deliberately does NOT import the MCP go-sdk — the thin
// adapter in internal/mcp wires these results to the wire protocol — so this logic stays
// SDK-version-independent and fully unit-testable.
package mcpsvc

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/events"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Service holds the registries the four MCP tools operate over.
type Service struct {
	agents    *agent.Registry
	upstreams *upstream.Registry
	policy    *policy.Registry
	access    *access.Registry
	pub       events.Publisher
}

// New constructs the domain service.
func New(a *agent.Registry, u *upstream.Registry, p *policy.Registry, ac *access.Registry) *Service {
	return &Service{agents: a, upstreams: u, policy: p, access: ac}
}

// SetPublisher attaches a (nil-safe) event publisher. RequestAccess publishes "access.requested"
// after logging the intent. Passing nil disables publishing. The access-request Create happens
// on this MCP path (not the admin API), so the bus is injected here rather than into the daemon
// admin handlers (see ADR-0005).
func (s *Service) SetPublisher(p events.Publisher) { s.pub = p }

// UpstreamInfo describes a known upstream and the agent's status against it.
// Status: open | needs-request | denied.
type UpstreamInfo struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Status  string `json:"status"`
}

// AccessResult is the outcome of request_access / get_access.
// Status: granted | pending-approval | denied.
type AccessResult struct {
	Status   string `json:"status"`
	BasePath string `json:"base_path"`
	Memo     string `json:"memo"`
}

// Identity is the whoami payload (the agent's own view of itself).
type Identity struct {
	AgentID  string   `json:"agent_id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Accesses []string `json:"accesses"`
}

// internal status values (single source of truth for derivation).
const (
	stOpen         = "open"
	stNeedsRequest = "needs-request"
	stDenied       = "denied"
)

// statusFor derives the agent's status against an upstream from the policy rules.
// Only rules whose subject is the agent or "" (any) are considered.
//   - any agent-tier (subject==agentID) deny  ⇒ denied
//   - else any allow / require-approval rule   ⇒ open
//   - else                                     ⇒ needs-request
//
// It also returns the matching allow/approval rules (for the get_access memo).
func (s *Service) statusFor(agentID, upstreamID string) (string, []*policy.Rule, error) {
	rules, err := s.policy.ForUpstream(upstreamID)
	if err != nil {
		return "", nil, fmt.Errorf("load rules: %w", err)
	}
	var allowing []*policy.Rule
	agentDeny := false
	hasAllow := false
	for _, r := range rules {
		if r.SubjectAgentID != "" && r.SubjectAgentID != agentID {
			continue // a rule for a different agent
		}
		switch r.Outcome {
		case policy.Deny:
			if r.SubjectAgentID == agentID {
				agentDeny = true
			}
		case policy.Allow, policy.RequireApproval:
			hasAllow = true
			allowing = append(allowing, r)
		}
	}
	switch {
	case agentDeny:
		return stDenied, allowing, nil
	case hasAllow:
		return stOpen, allowing, nil
	default:
		return stNeedsRequest, allowing, nil
	}
}

// toAccessResult maps an internal status to an AccessResult, filling BasePath/Memo.
func toAccessResult(internal, upstreamName string, allowing []*policy.Rule) AccessResult {
	switch internal {
	case stOpen:
		memos := make([]string, 0, len(allowing))
		for _, r := range allowing {
			method := r.Method
			if method == "" {
				method = "*"
			}
			memos = append(memos, method+" "+r.PathGlob)
		}
		return AccessResult{
			Status:   "granted",
			BasePath: "/" + upstreamName,
			Memo:     strings.Join(memos, ", "),
		}
	case stDenied:
		return AccessResult{Status: "denied", Memo: "access denied by an operator rule"}
	default:
		return AccessResult{
			Status: "pending-approval",
			Memo:   "no rule grants this yet — the operator has been notified",
		}
	}
}

// resolveUpstream finds an upstream by name, or by base-URL host match.
func (s *Service) resolveUpstream(hostOrUpstream string) (*upstream.Upstream, error) {
	up, err := s.upstreams.GetByName(hostOrUpstream)
	if err == nil {
		return up, nil
	}
	if err != upstream.ErrNotFound {
		return nil, err
	}
	// Fall back to host matching. Strip a scheme if the agent passed a full URL.
	want := hostOrUpstream
	if strings.Contains(want, "://") {
		if u, perr := url.Parse(want); perr == nil && u.Hostname() != "" {
			want = u.Hostname()
		}
	}
	ups, err := s.upstreams.List()
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	for _, u := range ups {
		parsed, perr := url.Parse(u.BaseURL)
		if perr != nil {
			continue
		}
		if parsed.Hostname() == want {
			return u, nil
		}
	}
	return nil, upstream.ErrNotFound
}

// ListUpstreams returns every known upstream with the agent's derived status.
func (s *Service) ListUpstreams(agentID string) ([]UpstreamInfo, error) {
	ups, err := s.upstreams.List()
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	out := make([]UpstreamInfo, 0, len(ups))
	for _, u := range ups {
		st, _, err := s.statusFor(agentID, u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, UpstreamInfo{Name: u.Name, BaseURL: u.BaseURL, Status: st})
	}
	return out, nil
}

// RequestAccess resolves the upstream, logs the agent's intent (with purpose), and returns the
// current rule-derived status. An unknown upstream is denied and NOT logged.
func (s *Service) RequestAccess(agentID, hostOrUpstream, purpose string) (AccessResult, error) {
	up, err := s.resolveUpstream(hostOrUpstream)
	if err == upstream.ErrNotFound {
		return AccessResult{Status: "denied", Memo: "no such upstream — ask the operator to add it"}, nil
	}
	if err != nil {
		return AccessResult{}, err
	}
	rec, err := s.access.Create(agentID, up.ID, purpose)
	if err != nil {
		return AccessResult{}, fmt.Errorf("log access request: %w", err)
	}
	if s.pub != nil {
		s.pub.Publish("access.requested", map[string]any{
			"id": rec.ID, "agent_id": agentID, "upstream_id": up.ID,
			"upstream_name": up.Name, "purpose": purpose,
		})
	}
	st, allowing, err := s.statusFor(agentID, up.ID)
	if err != nil {
		return AccessResult{}, err
	}
	return toAccessResult(st, up.Name, allowing), nil
}

// GetAccess reports the current rule-derived status for an upstream without logging intent.
func (s *Service) GetAccess(agentID, upstreamName string) (AccessResult, error) {
	up, err := s.resolveUpstream(upstreamName)
	if err == upstream.ErrNotFound {
		return AccessResult{Status: "denied", Memo: "no such upstream — ask the operator to add it"}, nil
	}
	if err != nil {
		return AccessResult{}, err
	}
	st, allowing, err := s.statusFor(agentID, up.ID)
	if err != nil {
		return AccessResult{}, err
	}
	return toAccessResult(st, up.Name, allowing), nil
}

// WhoAmI returns the agent's own identity and the upstreams it currently has open.
func (s *Service) WhoAmI(agentID string) (Identity, error) {
	a, err := s.agents.GetByID(agentID)
	if err != nil {
		return Identity{}, fmt.Errorf("load agent: %w", err)
	}
	ups, err := s.upstreams.List()
	if err != nil {
		return Identity{}, fmt.Errorf("list upstreams: %w", err)
	}
	accesses := []string{}
	for _, u := range ups {
		st, _, err := s.statusFor(agentID, u.ID)
		if err != nil {
			return Identity{}, err
		}
		if st == stOpen {
			accesses = append(accesses, u.Name)
		}
	}
	return Identity{AgentID: a.ID, Name: a.Name, Status: a.Status, Accesses: accesses}, nil
}
