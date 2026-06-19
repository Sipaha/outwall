package optemplate

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatch(t *testing.T) {
	tmpl, err := Parse("GET", "/api/v4/projects/{project_path:text}/pipelines",
		map[string]string{"updated_after": "{since:date}"})
	require.NoError(t, err)

	q := func(raw string) url.Values {
		u, perr := url.ParseQuery(raw)
		require.NoError(t, perr)
		return u
	}

	cases := []struct {
		name   string
		method string
		path   string
		query  url.Values
		wantOK bool
		vars   map[string]string
	}{
		{
			name:   "exact match extracts vars, %2F preserved in one segment",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q("updated_after=2026-06-01"),
			wantOK: true,
			vars:   map[string]string{"project_path": "infra/helm", "since": "2026-06-01"},
		},
		{
			name:   "wrong method does not match",
			method: "POST",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q("updated_after=2026-06-01"),
			wantOK: false,
		},
		{
			name:   "extra path segment does not over-capture",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines/9",
			query:  q("updated_after=2026-06-01"),
			wantOK: false,
		},
		{
			name:   "fewer path segments does not match",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm",
			query:  q("updated_after=2026-06-01"),
			wantOK: false,
		},
		{
			name:   "different literal segment does not match",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/builds",
			query:  q("updated_after=2026-06-01"),
			wantOK: false,
		},
		{
			name:   "undeclared non-exempt query param denies the match",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q("updated_after=2026-06-01&secret=1"),
			wantOK: false,
		},
		{
			name:   "exempt pagination param still matches",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q("updated_after=2026-06-01&page=2"),
			wantOK: true,
			vars:   map[string]string{"project_path": "infra/helm", "since": "2026-06-01"},
		},
		{
			name:   "missing declared query param does not match",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q(""),
			wantOK: false,
		},
		{
			name:   "non-date value for a date placeholder does not match",
			method: "GET",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q("updated_after=foo"),
			wantOK: false,
		},
		{
			name:   "case-insensitive method",
			method: "get",
			path:   "/api/v4/projects/infra%2Fhelm/pipelines",
			query:  q("updated_after=2026-06-01"),
			wantOK: true,
			vars:   map[string]string{"project_path": "infra/helm", "since": "2026-06-01"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars, ok := tmpl.Match(tc.method, tc.path, tc.query)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, tc.vars, vars)
			}
		})
	}
}

func TestMatchLiteralQuery(t *testing.T) {
	// A query template with a literal value: the request must carry exactly that value.
	tmpl, err := Parse("GET", "/search", map[string]string{"scope": "issues"})
	require.NoError(t, err)

	_, ok := tmpl.Match("GET", "/search", url.Values{"scope": {"issues"}})
	require.True(t, ok)
	_, ok = tmpl.Match("GET", "/search", url.Values{"scope": {"merge_requests"}})
	require.False(t, ok, "a literal query value must match exactly")
	_, ok = tmpl.Match("GET", "/search", url.Values{})
	require.False(t, ok, "a declared literal query param is required")
}

func TestMatchNoPlaceholders(t *testing.T) {
	// A fixed template (no placeholders) is just a fixed path; vars is empty on a match.
	tmpl, err := Parse("DELETE", "/admin/cache", nil)
	require.NoError(t, err)
	vars, ok := tmpl.Match("DELETE", "/admin/cache", url.Values{})
	require.True(t, ok)
	require.Empty(t, vars)
	_, ok = tmpl.Match("DELETE", "/admin/cache/x", url.Values{})
	require.False(t, ok)
}

func TestVars(t *testing.T) {
	tmpl, err := Parse("GET", "/p/{a:text}/{b:text}", map[string]string{"c": "{c:date}"})
	require.NoError(t, err)
	vars := tmpl.Vars()
	require.Equal(t, []Variable{
		{Name: "a", Type: Text},
		{Name: "b", Type: Text},
		{Name: "c", Type: Date},
	}, vars)
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		mth   string
		path  string
		query map[string]string
	}{
		{"unknown type", "GET", "/p/{a:bogus}", nil},
		{"malformed placeholder no type", "GET", "/p/{a}", nil},
		{"malformed placeholder empty name", "GET", "/p/{:text}", nil},
		{"duplicate var name path", "GET", "/p/{a:text}/{a:text}", nil},
		{"duplicate var name path+query", "GET", "/p/{a:text}", map[string]string{"q": "{a:text}"}},
		{"empty method", "", "/p", nil},
		{"placeholder spanning slash not allowed", "GET", "/p/{a:text}", map[string]string{"q": "{b}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.mth, tc.path, tc.query)
			require.Error(t, err)
		})
	}
}

func TestKeyStable(t *testing.T) {
	a, err := Parse("GET", "/p/{x:text}/q", map[string]string{"b": "{y:date}", "a": "lit"})
	require.NoError(t, err)
	// Same shape, query declared in a different map order → same key.
	b, err := Parse("GET", "/p/{x:text}/q", map[string]string{"a": "lit", "b": "{y:date}"})
	require.NoError(t, err)
	require.Equal(t, a.Key(), b.Key())

	// Different method → different key.
	c, err := Parse("POST", "/p/{x:text}/q", map[string]string{"b": "{y:date}", "a": "lit"})
	require.NoError(t, err)
	require.NotEqual(t, a.Key(), c.Key())

	// Different path → different key.
	d, err := Parse("GET", "/p/{x:text}/r", map[string]string{"b": "{y:date}", "a": "lit"})
	require.NoError(t, err)
	require.NotEqual(t, a.Key(), d.Key())
}

func TestIsNumber(t *testing.T) {
	good := []string{"0", "42", "-3", "3.14", "-0.5", "1e3"}
	for _, s := range good {
		require.True(t, IsNumber(s), "expected %q to be a number", s)
	}
	bad := []string{"", "abc", "1.2.3", "0x1f", "12px"}
	for _, s := range bad {
		require.False(t, IsNumber(s), "expected %q to NOT be a number", s)
	}
}

func TestMatchNumberAndEnum(t *testing.T) {
	tmpl, err := Parse("GET", "/items/{id:number}", map[string]string{"sort": "{order:enum}"})
	require.NoError(t, err)

	// number segment + enum query → extracted; enum accepts any value structurally
	vars, ok := tmpl.Match("GET", "/items/42", url.Values{"sort": {"sideways"}})
	require.True(t, ok)
	require.Equal(t, map[string]string{"id": "42", "order": "sideways"}, vars)

	// a non-numeric number segment → no structural match
	_, ok = tmpl.Match("GET", "/items/abc", url.Values{"sort": {"asc"}})
	require.False(t, ok)
}

func TestIsDate(t *testing.T) {
	good := []string{
		"2026-06-01",
		"2026-06-01T12:30:00Z",
		"2026-06-01T12:30:00+02:00",
		"2026-06-01 12:30:00",
	}
	for _, s := range good {
		require.True(t, IsDate(s), "expected %q to be a date", s)
	}
	bad := []string{"infra/helm", "foo", "", "2026-13-99", "1"}
	for _, s := range bad {
		require.False(t, IsDate(s), "expected %q to NOT be a date", s)
	}
}
