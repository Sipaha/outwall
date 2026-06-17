package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"/repos/**", "/repos/x/y", true},
		{"/repos/**", "/repos", false}, // ** needs the trailing segment(s)
		{"/repos/*", "/repos/x", true},
		{"/repos/*", "/repos/x/y", false}, // * does not cross '/'
		{"/**", "/anything/at/all", true},
		{"/users/*/repos", "/users/bob/repos", true},
		{"/users/*/repos", "/users/bob/x/repos", false},
		{"**", "/a/b", true},
		{"/exact", "/exact", true},
		{"/exact", "/exacto", false},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, MatchGlob(c.pat, c.path), "pat=%q path=%q", c.pat, c.path)
	}
}
