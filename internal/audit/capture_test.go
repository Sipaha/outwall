package audit

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureTeesAndCaps(t *testing.T) {
	var gotStored []byte
	var gotTotal int64
	var gotTrunc bool
	c := NewCapture(io.NopCloser(strings.NewReader("hello world")), 5,
		func(stored []byte, total int64, truncated bool) {
			gotStored, gotTotal, gotTrunc = stored, total, truncated
		})
	out, err := io.ReadAll(c)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(out)) // full body passes through
	require.NoError(t, c.Close())
	require.Equal(t, "hello", string(gotStored)) // capped at 5
	require.Equal(t, int64(11), gotTotal)
	require.True(t, gotTrunc)
}

func TestClassifyNonText(t *testing.T) {
	b := ClassifyBody("response", "image/png", []byte("\x89PNG"), 9000, true)
	require.Nil(t, b.Stored) // non-text → no bytes
	require.Equal(t, int64(9000), b.Size)
	require.NotEmpty(t, b.Sha256)
	b2 := ClassifyBody("request", "application/json", []byte("{}"), 2, false)
	require.Equal(t, []byte("{}"), b2.Stored)
}
