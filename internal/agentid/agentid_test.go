package agentid_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
