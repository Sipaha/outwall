package citeck

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/Sipaha/outwall/internal/serverprofile"
)

func init() { serverprofile.Register("citeck", New()) }

// New returns the citeck server profile.
func New() serverprofile.Profile { return profile{} }

type profile struct{}

func (profile) Name() string { return "citeck" }

func (profile) Classify(r serverprofile.Request) (serverprofile.Operation, bool, error) {
	return classify(r)
}

func (profile) Presets() []serverprofile.Preset {
	slots := []serverprofile.PresetSlot{
		{Key: "sourceId", Label: "Source ID (glob)", Type: "text", AllowAny: true, Required: true},
		{Key: "workspace", Label: "Workspace", Type: "text", AllowAny: false, Required: true},
	}
	browse := serverprofile.RuleTemplate{Outcome: serverprofile.Allow, BrowseMethods: "GET,HEAD", BrowsePath: "/**"}
	opTmpl := func(op string, b serverprofile.Bindings) (serverprofile.RuleTemplate, error) {
		params, err := json.Marshal(ruleParams{Op: op, SourceID: b["sourceId"], Workspace: b["workspace"]})
		if err != nil {
			return serverprofile.RuleTemplate{}, err
		}
		return serverprofile.RuleTemplate{Outcome: serverprofile.Allow, Profile: "citeck", ProfileParams: params}, nil
	}
	return []serverprofile.Preset{
		{
			ID: "citeck-readonly", Label: "ReadOnly", Slots: slots,
			Build: func(b serverprofile.Bindings) ([]serverprofile.RuleTemplate, error) {
				read, err := opTmpl("read", b)
				if err != nil {
					return nil, err
				}
				return []serverprofile.RuleTemplate{browse, read}, nil
			},
		},
		{
			ID: "citeck-readwrite", Label: "ReadWrite", Slots: slots,
			Build: func(b serverprofile.Bindings) ([]serverprofile.RuleTemplate, error) {
				read, err := opTmpl("read", b)
				if err != nil {
					return nil, err
				}
				write, err := opTmpl("write", b)
				if err != nil {
					return nil, err
				}
				return []serverprofile.RuleTemplate{browse, read, write}, nil
			},
		},
	}
}

func (profile) RuleSchema() serverprofile.RuleSchema {
	return serverprofile.RuleSchema{
		Profile: "citeck",
		Fields: []serverprofile.RuleField{
			{Key: "op", Label: "Operation", Type: "enum", Options: []string{"read", "write"}},
			{Key: "source_id", Label: "Source ID (glob)", Type: "text"},
			{Key: "workspace", Label: "Workspace (glob; not enforced for update/delete)", Type: "text"},
		},
	}
}

// ruleParams is the stored shape of a citeck rule's params blob.
type ruleParams struct {
	Op        string `json:"op"`        // "" | "read" | "write"
	SourceID  string `json:"source_id"` // glob; "" or "*" = any
	Workspace string `json:"workspace"` // glob; "" or "*" = any (ignored for update/delete)
}

func (profile) Match(rule serverprofile.Rule, op serverprofile.Operation) (string, bool, error) {
	var p ruleParams
	if err := json.Unmarshal(nonNil(rule.Params), &p); err != nil {
		return "", false, nil // a malformed rule never grants
	}
	if p.Op != "" && p.Op != op.Kind {
		return "", false, nil
	}
	if len(op.Resources) == 0 {
		return "", false, nil
	}
	for _, res := range op.Resources {
		if !matchSource(p.SourceID, res.Resource) || !matchWorkspace(p.Workspace, res.Scope) {
			return "", false, nil // every touched resource must pass
		}
	}
	return rule.Outcome, true, nil
}

func matchSource(ruleSrc, src string) bool {
	if ruleSrc == "" || ruleSrc == "*" {
		return true
	}
	return matchGlob(ruleSrc, src)
}

// matchWorkspace: an empty/"*" rule workspace matches anything (incl. all/unknown). A concrete rule
// workspace matches only a concrete request workspace via glob — never scopeAll/scopeUnknown (those
// cannot be proven to be within a specific workspace).
func matchWorkspace(ruleWs, scope string) bool {
	if ruleWs == "" || ruleWs == "*" {
		return true
	}
	switch scope {
	case scopeAll, scopeUnknown:
		return false
	default:
		return matchGlob(ruleWs, scope)
	}
}

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

// matchGlob: '*' matches within one '/'-delimited segment, '**' across segments.
func matchGlob(pattern, s string) bool {
	globMu.Lock()
	re, ok := globCache[pattern]
	globMu.Unlock()
	if !ok {
		var b strings.Builder
		b.WriteString("^")
		for i := 0; i < len(pattern); i++ {
			switch {
			case strings.HasPrefix(pattern[i:], "**"):
				b.WriteString(".*")
				i++
			case pattern[i] == '*':
				b.WriteString("[^/]*")
			default:
				b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			}
		}
		b.WriteString("$")
		compiled := regexp.MustCompile(b.String())
		globMu.Lock()
		globCache[pattern] = compiled
		globMu.Unlock()
		re = compiled
	}
	return re.MatchString(s)
}
