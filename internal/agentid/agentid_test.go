package agentid_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/agentid"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
}

func TestTokenPathGitTopLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := t.TempDir()
	runGit(t, repo, "init")
	sub := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	root, err := agentid.TokenPath(repo)
	require.NoError(t, err)
	child, err := agentid.TokenPath(sub)
	require.NoError(t, err)

	require.Equal(t, root, child, "a subdir of a repo maps to the same token path")
	require.True(t, strings.HasPrefix(root, filepath.Join(home, ".spk", "outwall", "agents")), root)
	require.True(t, strings.HasSuffix(root, ".token"), root)
}

func TestTokenPathNonRepoDiffers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d1 := t.TempDir()
	d2 := t.TempDir()

	p1, err := agentid.TokenPath(d1)
	require.NoError(t, err)
	p2, err := agentid.TokenPath(d2)
	require.NoError(t, err)
	require.NotEqual(t, p1, p2)
}

func TestLoadOrRegisterMintsOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir() // non-repo project

	var calls int32
	register := func(name string) (string, string, error) {
		n := atomic.AddInt32(&calls, 1)
		return "id-" + name, fmt.Sprintf("owa_tok_%d", n), nil
	}

	const N = 16
	var wg sync.WaitGroup
	tokens := make([]string, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = agentid.LoadOrRegister(dir, register)
		}(i)
	}
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i])
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "register must be called exactly once")
	for i := 1; i < N; i++ {
		require.Equal(t, tokens[0], tokens[i])
	}

	// A later call reads the persisted token without registering again.
	tok, err := agentid.LoadOrRegister(dir, func(string) (string, string, error) {
		t.Fatal("register must not be called when a token exists")
		return "", "", nil
	})
	require.NoError(t, err)
	require.Equal(t, tokens[0], tok)
}
