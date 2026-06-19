// Package mcpsvc is the SDK-free domain service behind the four MCP control-plane tools.
//
// It resolves a host/upstream, derives an agent's per-upstream status from the policy rules,
// and builds the tool responses. It deliberately does NOT import the MCP go-sdk — the thin
// adapter in internal/mcp wires these results to the wire protocol — so this logic stays
// SDK-version-independent and fully unit-testable.
package mcpsvc

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Sipaha/outwall/internal/access"
	"github.com/Sipaha/outwall/internal/agent"
	"github.com/Sipaha/outwall/internal/approval"
	"github.com/Sipaha/outwall/internal/events"
	"github.com/Sipaha/outwall/internal/k8s"
	"github.com/Sipaha/outwall/internal/optemplate"
	"github.com/Sipaha/outwall/internal/policy"
	"github.com/Sipaha/outwall/internal/upstream"
)

// Service holds the registries the MCP tools operate over.
type Service struct {
	agents    *agent.Registry
	upstreams *upstream.Registry
	policy    *policy.Registry
	access    *access.Registry
	approvals *approval.Queue
	pub       events.Publisher

	// kubeconfig assembly inputs (set by the daemon via SetKubeconfigParams):
	dataPlaneURL string // e.g. https://127.0.0.1:8080 (no trailing slash)
	caPEM        string
}

// New constructs the domain service.
func New(a *agent.Registry, u *upstream.Registry, p *policy.Registry, ac *access.Registry) *Service {
	return &Service{agents: a, upstreams: u, policy: p, access: ac}
}

// SetApprovals wires the blocking approval queue the host/operation MCP requests enqueue into.
// request_host_access / request_access park a Pending on this queue from a background goroutine
// (so the MCP call returns "pending" immediately, non-blocking); the operator resolves it via the
// admin API. A nil queue makes those two tools report "operator approval is not wired".
func (s *Service) SetApprovals(q *approval.Queue) { s.approvals = q }

// SetKubeconfigParams provides the data-plane base URL and local-CA PEM used to assemble
// agent kubeconfigs in Kubeconfig / get_kubeconfig.
func (s *Service) SetKubeconfigParams(dataPlaneURL, caPEM string) {
	s.dataPlaneURL = dataPlaneURL
	s.caPEM = caPEM
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
	Kind    string `json:"kind"`
	Status  string `json:"status"`
}

// AccessResult is the outcome of request_access / get_access.
// Status: granted | pending | denied.
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
			method := r.OpMethod
			if method == "" {
				method = "*"
			}
			memos = append(memos, method+" "+r.OpPathTemplate)
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
			Status: "pending",
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
		kind := u.Kind
		if kind == "" {
			kind = upstream.KindHTTP
		}
		out = append(out, UpstreamInfo{Name: u.Name, BaseURL: u.BaseURL, Kind: kind, Status: st})
	}
	return out, nil
}

// Kubeconfig assembles an agent kubeconfig for a k8s cluster, using the calling agent's own
// outwall token. The cluster's real credentials are never included. Errors if the named
// upstream is not a k8s cluster or kubeconfig params were not set.
func (s *Service) Kubeconfig(cluster, agentToken string) ([]byte, error) {
	up, err := s.upstreams.GetByName(cluster)
	if err != nil {
		return nil, fmt.Errorf("resolve cluster: %w", err)
	}
	if up.Kind != upstream.KindK8s {
		return nil, fmt.Errorf("%q is not a k8s cluster", cluster)
	}
	if s.dataPlaneURL == "" {
		return nil, fmt.Errorf("kubeconfig params not configured")
	}
	serverURL := s.dataPlaneURL + "/" + up.Name
	return k8s.Kubeconfig(serverURL, up.Name, s.caPEM, agentToken)
}

// Variable is a declared typed operation variable in a request_access call (name + "text"/"date").
type Variable struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// RequestAccessInput is the enriched request_access payload: the operation the agent needs
// (host + method + path/query templates + typed variables + the concrete values it intends to
// use) plus the purpose. Enforcement still parses the real request — these values only seed the
// operator's approval card and the allowed-set.
type RequestAccessInput struct {
	Host          string
	Method        string
	PathTemplate  string
	QueryTemplate map[string]string
	BodyTemplate  map[string]string // JSON dotted path -> literal or "{name:type}" (request body vars)
	Variables     []Variable
	Values        map[string]string
	Purpose       string
}

// RequestHostAccess is the tier-1 MCP host channel: it lazily registers the host as an upstream
// (credential-less) and enqueues a host approval carrying the purpose, then reports the current
// rule-derived status. The MCP call does not block — on "pending" the agent polls get_access.
func (s *Service) RequestHostAccess(agentID, host, purpose string) (AccessResult, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return AccessResult{}, fmt.Errorf("host is required")
	}
	up, _, err := s.upstreams.GetOrCreateByHost(host)
	if err != nil {
		return AccessResult{}, fmt.Errorf("resolve host upstream: %w", err)
	}
	// Log the intent (purpose) for the operator's access-request queue.
	if _, err := s.access.Create(agentID, up.ID, purpose); err != nil {
		return AccessResult{}, fmt.Errorf("log access request: %w", err)
	}
	st, allowing, err := s.statusFor(agentID, up.ID)
	if err != nil {
		return AccessResult{}, err
	}
	res := toAccessResult(st, up.Name, allowing)
	// Already open (an allow/approval rule exists) → granted, no need to enqueue a host card.
	if res.Status == "granted" {
		return res, nil
	}
	if err := s.enqueue(agentID, approval.Pending{
		Kind: approval.KindHostAccess, AgentID: agentID, UpstreamID: up.ID,
		Host: up.Name, Purpose: purpose,
	}); err != nil {
		return AccessResult{}, err
	}
	return res, nil
}

// RequestAccess is the tier-2 MCP operation channel: it validates the proposed operation template
// with optemplate.Parse (a malformed template is a tool error, NOT a pending), then enqueues an
// operation approval carrying the parsed shape + the requested values + purpose. The MCP call does
// not block — on "pending" the agent polls get_access. The host must already exist (tier-1 first).
func (s *Service) RequestAccess(agentID string, in RequestAccessInput) (AccessResult, error) {
	up, err := s.resolveUpstream(in.Host)
	if err == upstream.ErrNotFound {
		return AccessResult{Status: "denied", Memo: "no such host — request_host_access first"}, nil
	}
	if err != nil {
		return AccessResult{}, err
	}
	// Validate the template up front so a malformed shape errors at the tool boundary rather than
	// parking an unusable pending. Reparse normalizes to the same identity the H1 rule will use.
	tmpl, err := optemplate.ParseWithBody(in.Method, in.PathTemplate, in.QueryTemplate, in.BodyTemplate)
	if err != nil {
		return AccessResult{}, fmt.Errorf("invalid operation template: %w", err)
	}
	_ = tmpl // parsed only to validate; the rule is (re)created from the raw shape on approve.

	vars := make([]approval.Variable, 0, len(in.Variables))
	for _, v := range in.Variables {
		vars = append(vars, approval.Variable{Name: v.Name, Type: v.Type})
	}
	if err := s.enqueue(agentID, approval.Pending{
		Kind: approval.KindOperation, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
		Method: in.Method, Path: in.PathTemplate, Purpose: in.Purpose,
		OpMethod: in.Method, OpPathTemplate: in.PathTemplate, OpQueryTemplate: in.QueryTemplate,
		OpBodyTemplate: in.BodyTemplate,
		OpVariables:    vars, OpValues: in.Values,
	}); err != nil {
		return AccessResult{}, err
	}
	return AccessResult{Status: "pending", BasePath: "/" + up.Name,
		Memo: "operation submitted for approval — poll get_access"}, nil
}

// enqueue parks a Pending on the approval queue from a background goroutine so the MCP call
// returns immediately (the agent polls get_access). The queue must be wired via SetApprovals.
func (s *Service) enqueue(agentID string, p approval.Pending) error {
	if s.approvals == nil {
		return fmt.Errorf("operator approval is not wired")
	}
	go func() {
		// The decision is delivered to the daemon resolve path, which runs the side effects
		// (credential attach / rule create-extend) before unparking; we discard the bool here.
		_, _ = s.approvals.Submit(context.Background(), p)
	}()
	if s.pub != nil {
		s.pub.Publish("access.requested", map[string]any{
			"agent_id": agentID, "upstream_id": p.UpstreamID, "upstream_name": p.Host,
			"purpose": p.Purpose, "kind": p.Kind,
		})
	}
	return nil
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
