package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadPasswordFromStdin(t *testing.T) {
	// A single trailing newline is trimmed; interior content is preserved verbatim.
	pw, err := readPassword(strings.NewReader("hunter2\n"), true, "ignored: ")
	require.NoError(t, err)
	require.Equal(t, "hunter2", pw)

	// CRLF line ending is trimmed too.
	pw, err = readPassword(strings.NewReader("hunter2\r\n"), true, "ignored: ")
	require.NoError(t, err)
	require.Equal(t, "hunter2", pw)

	// No trailing newline: taken as-is.
	pw, err = readPassword(strings.NewReader("hunter2"), true, "ignored: ")
	require.NoError(t, err)
	require.Equal(t, "hunter2", pw)

	// Only the LAST newline is trimmed — an interior newline stays.
	pw, err = readPassword(strings.NewReader("a\nb\n"), true, "ignored: ")
	require.NoError(t, err)
	require.Equal(t, "a\nb", pw)
}
