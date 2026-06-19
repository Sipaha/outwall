package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenRejectsNonHTTPSchemes(t *testing.T) {
	// These must be rejected before any opener is spawned (no shell, no exec).
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "data:text/html,x", ""} {
		require.Error(t, Open(bad), "scheme of %q must be refused", bad)
	}
}
