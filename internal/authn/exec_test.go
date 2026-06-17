package authn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecTokenSourceCachesUntilExpiry(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "plugin.sh")
	// Each invocation appends an "x" to the counter file, then prints an ExecCredential
	// with a far-future expiry.
	body := "#!/bin/sh\n" +
		"printf x >> '" + counter + "'\n" +
		`echo '{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"exec-tok-123","expirationTimestamp":"2999-01-01T00:00:00Z"}}'` + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))

	src := newExecTokenSource("/bin/sh", []string{script}, nil)

	ctx := context.Background()
	tok1, err := src.Token(ctx)
	require.NoError(t, err)
	require.Equal(t, "exec-tok-123", tok1)

	tok2, err := src.Token(ctx)
	require.NoError(t, err)
	require.Equal(t, "exec-tok-123", tok2)

	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	require.Equal(t, "x", string(data), "plugin must be invoked exactly once and cached")
}

func TestExecTokenSourceRefreshesAfterExpiry(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "plugin.sh")
	// Print an already-expired credential so the cache is never reused.
	body := "#!/bin/sh\n" +
		`echo '{"kind":"ExecCredential","status":{"token":"fresh","expirationTimestamp":"2000-01-01T00:00:00Z"}}'` + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))

	src := newExecTokenSource("/bin/sh", []string{script}, nil)
	tok, err := src.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, "fresh", tok)
	// Even past-expiry tokens are returned (better than nothing), but not cached.
	require.True(t, src.expiry.Before(time.Now()))
}

func TestExecTokenSourceNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "plugin.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0o700))

	src := newExecTokenSource("/bin/sh", []string{script}, nil)
	_, err := src.Token(context.Background())
	require.Error(t, err)
}
