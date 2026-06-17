package policy

import "strings"

// Input is the request context evaluated against the rules.
type Input struct {
	AgentID, UpstreamID, Method, Path string
}

// Decision is the outcome of evaluating a request; Rule is the matched rule (nil on default-deny).
type Decision struct {
	Outcome string
	Rule    *Rule
}

func methodMatches(ruleMethod, reqMethod string) bool {
	return ruleMethod == "" || ruleMethod == "*" || strings.EqualFold(ruleMethod, reqMethod)
}

// Decide applies precedence: agent-specific rules outrank any-subject rules outrank
// default-deny; within the chosen tier, most-restrictive wins (deny > require-approval > allow).
func (r *Registry) Decide(in Input) (Decision, error) {
	rules, err := r.ForUpstream(in.UpstreamID)
	if err != nil {
		return Decision{}, err
	}
	var agentTier, anyTier []*Rule
	for _, rule := range rules {
		if !methodMatches(rule.Method, in.Method) || !MatchGlob(rule.PathGlob, in.Path) {
			continue
		}
		switch rule.SubjectAgentID {
		case in.AgentID:
			agentTier = append(agentTier, rule)
		case "":
			anyTier = append(anyTier, rule)
		}
	}
	if d, ok := resolveTier(agentTier); ok {
		return d, nil
	}
	if d, ok := resolveTier(anyTier); ok {
		return d, nil
	}
	return Decision{Outcome: Deny, Rule: nil}, nil // default-deny
}

// resolveTier picks the most-restrictive outcome among matched rules in one tier.
func resolveTier(tier []*Rule) (Decision, bool) {
	if len(tier) == 0 {
		return Decision{}, false
	}
	var deny, approval, allow *Rule
	for _, r := range tier {
		switch r.Outcome {
		case Deny:
			if deny == nil {
				deny = r
			}
		case RequireApproval:
			if approval == nil {
				approval = r
			}
		case Allow:
			if allow == nil {
				allow = r
			}
		}
	}
	switch {
	case deny != nil:
		return Decision{Outcome: Deny, Rule: deny}, true
	case approval != nil:
		return Decision{Outcome: RequireApproval, Rule: approval}, true
	default:
		return Decision{Outcome: Allow, Rule: allow}, true
	}
}
