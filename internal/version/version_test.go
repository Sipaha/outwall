package version

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStringNotEmpty(t *testing.T) {
	require.NotEmpty(t, String())
	require.Contains(t, String(), ".")
}
