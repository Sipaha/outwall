# module: internal/optemplate

The pure operation-template core of the HTTP policy engine (ADR-0014). It parses a
`(method, path-template, query-template)` into **segment-bounded typed placeholders** and matches
a real request against it, returning the extracted variable values. It has no dependencies beyond
the standard library and is fully unit-testable. Enforcement is by **parsing the real request** —
the package checks structure + variable types; the per-variable *value* policy (allowed-set /
`any`) is `internal/policy`'s job.

**Placeholders.** A path template uses `{name:type}` placeholders, each binding **exactly one**
path segment (no `/`). Types are `text` and `date`. A literal segment matches itself. A
query-template maps a param name to either a literal value or a `{name:type}` placeholder.

**Matching rules.** `Match(method, path, query)` fails (so the request does not match this
template, and policy treats that as a non-match) when:

- the method differs (case-insensitive);
- the path segment count differs, or a literal segment differs — placeholders never over-capture
  extra segments (the "no over-capture" guarantee, mirroring the k8s segment parser);
- a declared query param is absent, or (for a literal) differs;
- an **undeclared, non-exempt** query param is present (`ExemptQueryParams` —
  `page`/`per_page`/`pagination` — are tolerated; they are scope-neutral pagination and are still
  audited by the caller);
- a `date`-typed placeholder's extracted value fails `IsDate` (so a scope-bearing value cannot
  ride a `date` slot).

Each path segment is decoded per-segment (`url.PathUnescape`) so a GitLab `%2F` inside one
segment is preserved as the value (`infra%2Fhelm` → `infra/helm`), not split into two segments —
the proxy passes the **escaped** path so the split is on real `/` only.

`Key()` is a stable identity for a template (method + path-template + sorted query-template), so
two requests with the same shape map to one rule.

## Public API

- `VarType` with consts `Text = "text"`, `Date = "date"`.
- `Variable struct { Name string; Type VarType }`.
- `Template` (opaque parsed form).
- `ExemptQueryParams map[string]struct{}` — scope-neutral query params tolerated when undeclared.
- `Parse(method, pathTemplate string, queryTemplate map[string]string) (Template, error)` — errors on a malformed placeholder, an unknown type, a duplicate variable name, or an empty method.
- `(Template).Vars() []Variable` — declared variables, path then query, declaration order.
- `(Template).Key() string` — stable template identity.
- `(Template).Match(method, path string, query url.Values) (vars map[string]string, ok bool)` — structural match + typed extraction.
- `IsDate(s string) bool` — RFC3339 / `2006-01-02` / common date-time forms.
