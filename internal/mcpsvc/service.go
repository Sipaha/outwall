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
	"time"

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

	// subscribe, when set, returns a fresh event subscription (channel + cancel). get_access uses it
	// to long-poll: block until the agent's pending request is resolved (or a timeout) instead of
	// the agent busy-polling. nil disables long-poll (get_access answers immediately).
	subscribe func() (<-chan events.Event, func())

	// kubeconfig assembly inputs (set by the daemon via SetKubeconfigParams):
	dataPlaneURL string // e.g. https://127.0.0.1:8080 (no trailing slash)
	caPEM        string
}

// getAccessWait bounds how long get_access blocks waiting for an operator decision before returning
// the current (still-pending) status. Kept well under typical MCP client timeouts.
const getAccessWait = 25 * time.Second

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

// SetEvents wires an event-subscription factory (typically events.Bus.Subscribe) so get_access can
// long-poll for an operator decision. Passing nil disables long-poll.
func (s *Service) SetEvents(subscribe func() (<-chan events.Event, func())) { s.subscribe = subscribe }

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
	// The host tier exists only to register a host and attach its credential. For upstreams the
	// operator can take no useful credential action on — k8s clusters (pre-credentialed) and HTTP
	// hosts that already carry a credential — short-circuit with guidance: no card, no log row
	// (nothing for the operator to act on). See ADR-0025.
	if up.Kind == upstream.KindK8s {
		return AccessResult{
			Status:   "granted",
			BasePath: "/" + up.Name,
			Memo:     "k8s cluster — credentials already configured; call get_kubeconfig and request_k8s_access for the namespace/resource/verb you need",
		}, nil
	}
	if hasCredential(up) {
		return AccessResult{
			Status:   "granted",
			BasePath: "/" + up.Name,
			Memo:     "host already registered with a credential — call request_access for the operation you need",
		}, nil
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
	// Dedupe: a host request already awaiting a decision → don't log/enqueue a second one.
	if s.pendingExists(func(p approval.Pending) bool {
		return p.Kind == approval.KindHostAccess && p.AgentID == agentID && p.UpstreamID == up.ID
	}) {
		return res, nil
	}
	// Log the intent (purpose) for the operator's access-request queue.
	if _, err := s.access.Create(agentID, up.ID, purpose); err != nil {
		return AccessResult{}, fmt.Errorf("log access request: %w", err)
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

	// Dedupe: the same operation already awaiting a decision → don't log/enqueue a second one.
	if s.pendingExists(func(p approval.Pending) bool {
		return p.Kind == approval.KindOperation && p.AgentID == agentID && p.UpstreamID == up.ID &&
			p.OpMethod == in.Method && p.OpPathTemplate == in.PathTemplate
	}) {
		return AccessResult{Status: "pending", BasePath: "/" + up.Name,
			Memo: "operation already submitted — call get_access (it waits for the decision)"}, nil
	}

	// Log the intent so an operator deny (with a reason) has a request row to mark, which
	// get_access then surfaces back to this agent.
	if _, err := s.access.Create(agentID, up.ID, in.Purpose); err != nil {
		return AccessResult{}, fmt.Errorf("log access request: %w", err)
	}

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
		Memo: "operation submitted for approval — call get_access (it waits for the decision)"}, nil
}

// hasCredential reports whether an upstream already carries a usable credential, so a host-access
// request need not raise a credential-attach card. For k8s the credential is the cluster connection
// (handled by the caller); here it covers HTTP auth types.
func hasCredential(up *upstream.Upstream) bool {
	return up.AuthType != "" && up.AuthType != "none"
}

// RequestK8sAccess is the MCP k8s-access channel: an agent declares the (namespace, resource, verb)
// it needs on a registered k8s cluster. It validates the upstream is a k8s cluster, logs the intent,
// and enqueues a KindK8sAccess approval carrying the tuple. The MCP call does not block — on
// "pending" the agent polls get_access. On approve the resolve path creates an agent-scoped allow
// k8s rule for the tuple (see ADR-0025). k8s clusters are pre-credentialed, so there is no host tier.
func (s *Service) RequestK8sAccess(agentID, cluster, namespace, resource, verb, purpose string) (AccessResult, error) {
	up, err := s.resolveUpstream(cluster)
	if err == upstream.ErrNotFound {
		return AccessResult{Status: "denied", Memo: "no such cluster — ask the operator to register it"}, nil
	}
	if err != nil {
		return AccessResult{}, err
	}
	if up.Kind != upstream.KindK8s {
		return AccessResult{}, fmt.Errorf("%q is not a k8s cluster — use request_host_access / request_access", cluster)
	}
	// An allow/approval k8s rule already covering this agent → granted, no card.
	st, allowing, err := s.statusFor(agentID, up.ID)
	if err != nil {
		return AccessResult{}, err
	}
	if res := toAccessResult(st, up.Name, allowing); res.Status == "granted" {
		return res, nil
	}
	// Dedupe: an identical k8s request already awaiting a decision → don't raise a second card.
	if s.pendingExists(func(p approval.Pending) bool {
		return p.Kind == approval.KindK8sAccess && p.AgentID == agentID && p.UpstreamID == up.ID &&
			p.Namespace == namespace && p.Resource == resource && p.Verb == verb
	}) {
		return AccessResult{Status: "pending", BasePath: "/" + up.Name,
			Memo: "k8s access already submitted — call get_access (it waits for the decision)"}, nil
	}
	// Log the intent so a deny (with a reason) has a row to mark, surfaced via get_access.
	if _, err := s.access.Create(agentID, up.ID, purpose); err != nil {
		return AccessResult{}, fmt.Errorf("log access request: %w", err)
	}
	if err := s.enqueue(agentID, approval.Pending{
		Kind: approval.KindK8sAccess, AgentID: agentID, UpstreamID: up.ID, Host: up.Name,
		Namespace: namespace, Resource: resource, Verb: verb, Purpose: purpose,
	}); err != nil {
		return AccessResult{}, err
	}
	return AccessResult{Status: "pending", BasePath: "/" + up.Name,
		Memo: "k8s access submitted for approval — call get_access (it waits for the decision)"}, nil
}

// pendingExists reports whether the approval queue already holds a Pending matching `match`. Used to
// dedupe a repeated request so a second identical call does not raise a second approval card.
func (s *Service) pendingExists(match func(approval.Pending) bool) bool {
	if s.approvals == nil {
		return false
	}
	for _, p := range s.approvals.List() {
		if match(p) {
			return true
		}
	}
	return false
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

// GetAccess reports the current rule-derived status for an upstream without logging intent. When the
// agent has an outstanding pending request, it LONG-POLLS: it blocks until the operator decides (or
// getAccessWait elapses) instead of returning "pending" immediately — so the agent calls it once and
// waits rather than busy-polling.
func (s *Service) GetAccess(agentID, upstreamName string) (AccessResult, error) {
	up, err := s.resolveUpstream(upstreamName)
	if err == upstream.ErrNotFound {
		return AccessResult{Status: "denied", Memo: "no such upstream — ask the operator to add it"}, nil
	}
	if err != nil {
		return AccessResult{}, err
	}
	res, err := s.accessStatus(agentID, up)
	if err != nil {
		return AccessResult{}, err
	}
	if res.Status == "pending" && s.subscribe != nil && s.hasPendingRequest(agentID, up.ID) {
		return s.waitForDecision(agentID, up)
	}
	return res, nil
}

// accessStatus computes the current rule-derived AccessResult, surfacing a recent operator denial
// (with its reason) instead of a bare "pending" so the agent learns why and stops.
func (s *Service) accessStatus(agentID string, up *upstream.Upstream) (AccessResult, error) {
	st, allowing, err := s.statusFor(agentID, up.ID)
	if err != nil {
		return AccessResult{}, err
	}
	res := toAccessResult(st, up.Name, allowing)
	if res.Status != "granted" {
		if req, ok, lerr := s.access.Latest(agentID, up.ID); lerr == nil && ok && req.Status == access.StatusDenied {
			res.Status = "denied"
			if req.Reason != "" {
				res.Memo = "denied by the operator: " + req.Reason
			} else {
				res.Memo = "denied by the operator"
			}
		}
	}
	return res, nil
}

// hasPendingRequest reports whether the agent has an outstanding (pending) access request for the
// upstream — the condition under which get_access blocks waiting for a decision.
func (s *Service) hasPendingRequest(agentID, upID string) bool {
	req, ok, err := s.access.Latest(agentID, upID)
	return err == nil && ok && req.Status == access.StatusPending
}

// waitForDecision blocks until the agent's pending request for up resolves (status no longer
// "pending") or getAccessWait elapses, returning the latest status. It re-checks on each approval
// event and on a periodic tick (a safety net against a dropped event), and checks once up front to
// close the subscribe race.
func (s *Service) waitForDecision(agentID string, up *upstream.Upstream) (AccessResult, error) {
	ch, cancel := s.subscribe()
	defer cancel()
	deadline := time.NewTimer(getAccessWait)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		res, err := s.accessStatus(agentID, up)
		if err != nil {
			return AccessResult{}, err
		}
		if res.Status != "pending" {
			return res, nil
		}
		select {
		case <-ch:
		case <-tick.C:
		case <-deadline.C:
			return s.accessStatus(agentID, up)
		}
	}
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
