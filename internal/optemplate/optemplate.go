// Package optemplate is the pure operation-template core of the HTTP policy engine: it parses
// a (method, path-template, query-template) into segment-bounded typed placeholders and Matches
// a real request against it, returning the extracted variable values. Enforcement is by parsing
// the real request — never by trusting what the agent declared. It checks STRUCTURE and variable
// TYPES; the per-variable value policy (allowed-set / any) is the policy package's job.
package optemplate

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// VarType is a typed placeholder kind.
type VarType string

// Supported placeholder types (§5 of the design). text is gated by an allowed-set that grows via
// approval; date auto-allows but the extracted value must parse as a date; number requires the
// value to parse as a number (gated by a range in policy); enum extracts any value but is gated by
// a CLOSED allowed-set in policy (an out-of-set value is denied, not approved).
const (
	Text   VarType = "text"
	Date   VarType = "date"
	Number VarType = "number"
	Enum   VarType = "enum"
)

// Variable is a typed placeholder declared in a template.
type Variable struct {
	Name string
	Type VarType
}

// ExemptQueryParams are scope-neutral query params allowed even when undeclared (pagination).
// They are not extracted and not gated, but are still audited by the caller (§7).
var ExemptQueryParams = map[string]struct{}{
	"page":       {},
	"per_page":   {},
	"pagination": {},
}

// segment is one path-template segment: either a literal (placeholder nil) or a typed placeholder.
type segment struct {
	literal     string
	placeholder *Variable
}

// queryParam is one declared query param: either a literal value or a typed placeholder.
type queryParam struct {
	name        string
	literal     string
	placeholder *Variable
}

// Template is a parsed operation shape. Query params NOT named here are scope-bearing and cause a
// request to NOT match (except ExemptQueryParams).
type Template struct {
	method   string // upper-cased
	rawPath  string // the original path template, for Key()
	segments []segment
	query    []queryParam // sorted by name for stable Key()/iteration
	vars     []Variable   // path then query, declaration order
}

// Parse builds a Template. pathTemplate uses {name:type} placeholders, each binding exactly ONE
// path segment (no '/'); a literal segment matches itself. queryTemplate maps a param name to
// either a literal value or a "{name:type}" placeholder. Returns an error on a malformed
// placeholder, an unknown type, a duplicate variable name, or an empty method.
func Parse(method, pathTemplate string, queryTemplate map[string]string) (Template, error) {
	if strings.TrimSpace(method) == "" {
		return Template{}, fmt.Errorf("optemplate: empty method")
	}
	t := Template{
		method:  strings.ToUpper(method),
		rawPath: pathTemplate,
	}
	seen := map[string]struct{}{}

	for _, raw := range splitPath(pathTemplate) {
		if v, ok, err := parsePlaceholder(raw); err != nil {
			return Template{}, err
		} else if ok {
			if _, dup := seen[v.Name]; dup {
				return Template{}, fmt.Errorf("optemplate: duplicate variable %q", v.Name)
			}
			seen[v.Name] = struct{}{}
			t.segments = append(t.segments, segment{placeholder: &v})
			t.vars = append(t.vars, v)
		} else {
			t.segments = append(t.segments, segment{literal: raw})
		}
	}

	// Sort query keys for a deterministic Key() and iteration order.
	names := make([]string, 0, len(queryTemplate))
	for k := range queryTemplate {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		val := queryTemplate[name]
		if v, ok, err := parsePlaceholder(val); err != nil {
			return Template{}, err
		} else if ok {
			if _, dup := seen[v.Name]; dup {
				return Template{}, fmt.Errorf("optemplate: duplicate variable %q", v.Name)
			}
			seen[v.Name] = struct{}{}
			t.query = append(t.query, queryParam{name: name, placeholder: &v})
			t.vars = append(t.vars, v)
		} else {
			t.query = append(t.query, queryParam{name: name, literal: val})
		}
	}
	return t, nil
}

// parsePlaceholder parses a "{name:type}" token. Returns (Variable, true, nil) for a valid
// placeholder, (zero, false, nil) for a plain literal, and an error for a malformed placeholder
// (a value that opens with '{' or contains '{' but is not a well-formed single placeholder).
func parsePlaceholder(s string) (Variable, bool, error) {
	if !strings.Contains(s, "{") && !strings.Contains(s, "}") {
		return Variable{}, false, nil
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return Variable{}, false, fmt.Errorf("optemplate: malformed placeholder %q", s)
	}
	inner := s[1 : len(s)-1]
	if strings.ContainsAny(inner, "{}") {
		return Variable{}, false, fmt.Errorf("optemplate: malformed placeholder %q", s)
	}
	name, typ, ok := strings.Cut(inner, ":")
	if !ok {
		return Variable{}, false, fmt.Errorf("optemplate: placeholder %q missing :type", s)
	}
	if name == "" {
		return Variable{}, false, fmt.Errorf("optemplate: placeholder %q has empty name", s)
	}
	vt := VarType(typ)
	if vt != Text && vt != Date && vt != Number && vt != Enum {
		return Variable{}, false, fmt.Errorf("optemplate: unknown variable type %q in %q", typ, s)
	}
	return Variable{Name: name, Type: vt}, true, nil
}

// Vars returns the template's declared variables (path then query, declaration order).
func (t Template) Vars() []Variable {
	out := make([]Variable, len(t.vars))
	copy(out, t.vars)
	return out
}

// Key is a stable identity string for the template (method + normalized path-template + sorted
// query-template) — two requests with the same shape share one rule.
func (t Template) Key() string {
	var b strings.Builder
	b.WriteString(t.method)
	b.WriteByte(' ')
	b.WriteString(t.rawPath)
	b.WriteByte('?')
	for i, qp := range t.query { // already sorted by name
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(qp.name)
		b.WriteByte('=')
		if qp.placeholder != nil {
			b.WriteString("{:" + string(qp.placeholder.Type) + "}")
		} else {
			b.WriteString(qp.literal)
		}
	}
	return b.String()
}

// Match reports whether method+path+query fit the template's STRUCTURE and, if so, returns the
// extracted variable values (name -> raw decoded value). It does NOT check value policies (that
// is policy's job). Match fails if: method differs; segment count differs; a literal segment
// differs; a declared query param is absent or (for a literal) differs; or an undeclared,
// non-exempt query param is present. A Date-typed placeholder additionally requires the value to
// parse as a date.
func (t Template) Match(method, path string, query url.Values) (vars map[string]string, ok bool) {
	if !strings.EqualFold(method, t.method) {
		return nil, false
	}
	segs := splitPath(path)
	if len(segs) != len(t.segments) {
		return nil, false
	}
	out := map[string]string{}
	for i, seg := range t.segments {
		if seg.placeholder == nil {
			if segs[i] != seg.literal {
				return nil, false
			}
			continue
		}
		// Decode the single segment so an encoded '/' (%2F) is preserved as the value, not a
		// path delimiter — segment splitting already happened on the raw '/'.
		val, err := url.PathUnescape(segs[i])
		if err != nil {
			return nil, false
		}
		if !typeValid(seg.placeholder.Type, val) {
			return nil, false
		}
		out[seg.placeholder.Name] = val
	}

	// Declared query params must each be present and (for literals) equal; placeholders extract.
	declared := map[string]struct{}{}
	for _, qp := range t.query {
		declared[qp.name] = struct{}{}
		vals, present := query[qp.name]
		if !present || len(vals) == 0 {
			return nil, false
		}
		val := vals[0]
		if qp.placeholder == nil {
			if val != qp.literal {
				return nil, false
			}
			continue
		}
		if !typeValid(qp.placeholder.Type, val) {
			return nil, false
		}
		out[qp.placeholder.Name] = val
	}

	// Any undeclared, non-exempt query param denies the match (scope-bearing surface).
	for name := range query {
		if _, ok := declared[name]; ok {
			continue
		}
		if _, exempt := ExemptQueryParams[name]; exempt {
			continue
		}
		return nil, false
	}

	return out, true
}

// dateLayouts are the supported date / datetime forms (§5: ISO-8601 / common forms).
var dateLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

// typeValid reports whether a raw extracted value is admissible for the placeholder type at the
// STRUCTURAL level (Match's job). text/enum accept any value (their value gate is policy's job);
// date must parse as a date; number must parse as a number. A type-invalid value makes the request
// not match the template (it then falls through to default-deny — never a silent grant).
func typeValid(t VarType, val string) bool {
	switch t {
	case Date:
		return IsDate(val)
	case Number:
		return IsNumber(val)
	default: // Text, Enum
		return true
	}
}

// IsNumber reports whether s parses as an integer or a floating-point number. Exposed for policy +
// tests. Rejects empty and non-numeric strings.
func IsNumber(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// IsDate reports whether s parses as a supported date/datetime. Exposed for policy + tests.
func IsDate(s string) bool {
	if s == "" {
		return false
	}
	for _, layout := range dateLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// splitPath splits a path on '/', dropping empty leading/trailing segments. It does NOT decode
// segments (Match decodes per-placeholder so %2F stays inside one segment).
func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}
