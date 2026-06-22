package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowseDomainDefault(t *testing.T) {
	d := newDaemon(t)
	require.Equal(t, "outwall.localhost", d.cfg.BrowseDomain)
}
