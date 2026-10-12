package gittest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	Main(m)
}

func TestHarnessConfinesTheRun(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(Root(), "fixture"), cwd)
	require.Equal(t, Root(), os.Getenv("GIT_CEILING_DIRECTORIES"))
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"} {
		_, set := os.LookupEnv(name)
		require.False(t, set, name)
	}
	r := NewRepo(t)
	require.Equal(t, r.Dir, r.Git("rev-parse", "--show-toplevel"))
}

// The canary check must notice each kind of damage a stray stash operation
// can do. Each case plants canaries in a repository of its own.
func TestCanariesDetectDamage(t *testing.T) {
	damage := map[string]func(dir string){
		"stash dropped": func(dir string) { mustGit(t, dir, "stash", "drop", "-q") },
		"stash pushed":  func(dir string) { mustGit(t, dir, "stash", "push", "-q", "-u") },
		"stash popped":  func(dir string) { mustGit(t, dir, "reset", "-q", "--hard"); mustGit(t, dir, "stash", "pop", "-q") },
		"change reset":  func(dir string) { mustGit(t, dir, "checkout", "-q", "--", "tracked.txt") },
		"untracked removed": func(dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "untracked.txt")))
		},
		"stow entry added": func(dir string) {
			mustGit(t, dir, "update-ref", "refs/worktree/stow", "HEAD")
		},
	}
	for name, damage := range damage {
		t.Run(name, func(t *testing.T) {
			c, err := plantCanaries(filepath.Join(tempDir(t), "canary"))
			require.NoError(t, err)
			require.NoError(t, c.verify())
			damage(c.dir)
			require.Error(t, c.verify())
		})
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_, err := gitIn(dir, args...)
	require.NoError(t, err)
}
