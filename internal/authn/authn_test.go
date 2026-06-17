package authn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/upstream"
)

func TestApply(t *testing.T) {
	cases := []struct {
		name   string
		cfg    upstream.AuthConfig
		assert func(t *testing.T, r *http.Request)
	}{
		{"none", upstream.AuthConfig{Type: "none"}, func(t *testing.T, r *http.Request) {
			require.Empty(t, r.Header.Get("Authorization"))
		}},
		{"static", upstream.AuthConfig{Type: "static", Header: "X-API-Key", Token: "k123"},
			func(t *testing.T, r *http.Request) {
				require.Equal(t, "k123", r.Header.Get("X-API-Key"))
			}},
		{"basic", upstream.AuthConfig{Type: "basic", Username: "u", Password: "p"},
			func(t *testing.T, r *http.Request) {
				gotU, gotP, ok := r.BasicAuth()
				require.True(t, ok)
				require.Equal(t, "u", gotU)
				require.Equal(t, "p", gotP)
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := For(tc.cfg)
			require.NoError(t, err)
			r, _ := http.NewRequest(http.MethodGet, "https://x", nil)
			require.NoError(t, a.Apply(r))
			tc.assert(t, r)
		})
	}

	_, err := For(upstream.AuthConfig{Type: "weird"})
	require.ErrorIs(t, err, ErrUnsupported)
}
