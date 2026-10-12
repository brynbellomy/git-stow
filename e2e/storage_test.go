package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brynbellomy/git-stow/internal/gittest"
)

func TestStowRefAndReflogArePerWorktree(t *testing.T) {
	c := newClone(t)
	main, one, two := c.repos["main"], c.repos["one"], c.repos["two"]

	one.Write("a.txt", "one\n")
	one.MustStow("push", "-m", "first")
	one.Write("a.txt", "one again\n")
	one.MustStow("push", "-m", "second")

	// The ref and its reflog live in the linked worktree's private git dir.
	oneGitDir := one.Git("rev-parse", "--absolute-git-dir")
	require.NotEqual(t, main.Git("rev-parse", "--absolute-git-dir"), oneGitDir)
	require.FileExists(t, filepath.Join(oneGitDir, "refs", "worktree", "stow"))
	require.FileExists(t, filepath.Join(oneGitDir, "logs", "refs", "worktree", "stow"))

	// Neither is visible from the main checkout or another linked worktree.
	for _, other := range []*gittest.Repo{main, two} {
		require.Equal(t, 1, other.Run("", "", "git", "rev-parse", "--verify", "--quiet", "refs/worktree/stow").Code)
		require.NotEqual(t, 0, other.Run("", "", "git", "reflog", "show", "refs/worktree/stow").Code)
		require.Empty(t, other.Git("for-each-ref", "refs/worktree/"))
		require.Empty(t, stowList(other))
	}

	// The main checkout has a stack of its own.
	main.Write("a.txt", "main\n")
	main.MustStow("push", "-m", "main entry")
	mainGitDir := main.Git("rev-parse", "--absolute-git-dir")
	require.FileExists(t, filepath.Join(mainGitDir, "refs", "worktree", "stow"))
	require.Equal(t, "stow@{0}: On main: main entry", stowList(main))
	require.Equal(t, "stow@{0}: On one: second\nstow@{1}: On one: first", stowList(one))

	// git's own worktree-qualified names reach another worktree's stack.
	require.Equal(t, main.Git("rev-parse", "refs/worktree/stow"), two.Git("rev-parse", "main-worktree/refs/worktree/stow"))
	require.Equal(t, one.Git("rev-parse", "refs/worktree/stow@{1}"), two.Git("rev-parse", "worktrees/one/refs/worktree/stow@{1}"))
	require.Equal(t, "On main: main entry", two.Git("log", "-g", "--format=%gs", "main-worktree/refs/worktree/stow"))
	require.Equal(t, "On one: second\nOn one: first", two.Git("log", "-g", "--format=%gs", "worktrees/one/refs/worktree/stow"))

	// Inside a worktree, git itself names stow@{n} worktree/stow@{n}.
	require.Equal(t, "a.txt | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)", strings.TrimSpace(one.Git("stash", "show", "worktree/stow@{1}")))
}

func TestEntriesAreGitStashCommits(t *testing.T) {
	r := gittest.NewRepo(t)
	base(r)
	head := r.Git("rev-parse", "HEAD")
	r.Write("a.txt", "staged\n")
	r.Git("add", "a.txt")
	r.Write("b.txt", "unstaged\n")
	r.Write("new.txt", "untracked\n")
	r.MustStow("push", "-u")

	w := r.Git("rev-parse", "refs/worktree/stow")
	require.Equal(t, head+" "+r.Git("rev-parse", w+"^2")+" "+r.Git("rev-parse", w+"^3"), r.Git("log", "-1", "--format=%P", w))
	require.Equal(t, head, r.Git("rev-parse", w+"^2^"))
	require.Empty(t, r.Git("log", "-1", "--format=%P", w+"^3"))
	require.Equal(t, "staged", r.Git("show", w+"^2:a.txt"))
	require.Equal(t, "unstaged", r.Git("show", w+":b.txt"))
	require.Equal(t, "untracked", r.Git("show", w+"^3:new.txt"))

	// git stash itself reads and applies the entry.
	show := r.Git("stash", "show", "--include-untracked", "--name-only", w)
	require.Equal(t, []string{"a.txt", "b.txt", "new.txt"}, strings.Split(show, "\n"))
	r.Git("stash", "apply", "--index", w)
	require.Equal(t, "M  a.txt\n M b.txt\n?? new.txt", r.Git("status", "--porcelain"))
}

func TestEntriesSurviveGC(t *testing.T) {
	c := newClone(t)
	var entries []string
	for _, name := range c.names {
		r := c.repos[name]
		for _, content := range []string{"first", "second"} {
			r.Write("a.txt", name+" "+content+"\n")
			r.Write("new.txt", name+" "+content+"\n")
			r.MustStow("push", "-u")
		}
		entries = append(entries, r.Git("rev-parse", "refs/worktree/stow@{1}"), r.Git("rev-parse", "refs/worktree/stow@{0}"))
	}

	for _, name := range c.names {
		c.repos[name].Git("gc", "--quiet", "--prune=now")
	}
	main := c.repos["main"]
	for _, w := range entries {
		for _, obj := range []string{w, w + "^2", w + "^3", w + "^{tree}", w + "^3^{tree}"} {
			require.Equal(t, 0, main.Run("", "", "git", "cat-file", "-e", obj).Code, "%s was pruned", obj)
		}
	}
	for _, name := range c.names {
		require.Len(t, strings.Split(stowList(c.repos[name]), "\n"), 2, name)
	}
}

// git never expires refs/stash entries unless configured to; every other
// reflog loses entries after gc.reflogExpireUnreachable. git stow's first
// push configures the same exemption for every worktree's stack.
func TestEntriesSurviveReflogExpiry(t *testing.T) {
	c := newClone(t)
	old := []string{"GIT_COMMITTER_DATE=2001-01-01T00:00:00 +0000", "GIT_AUTHOR_DATE=2001-01-01T00:00:00 +0000"}
	for _, name := range c.names {
		r := c.repos[name].WithEnv(old...)
		for _, content := range []string{"first", "second", "third"} {
			r.Write("a.txt", name+" "+content+"\n")
			r.MustStow("push")
		}
	}
	main := c.repos["main"]
	require.Equal(t, "never", main.Git("config", "gc.*refs/worktree/stow.reflogExpire"))
	require.Equal(t, "never", main.Git("config", "gc.*refs/worktree/stow.reflogExpireUnreachable"))

	c.repos["two"].Git("reflog", "expire", "--all")
	main.Git("gc", "--quiet", "--prune=now")
	for _, name := range c.names {
		require.Len(t, strings.Split(stowList(c.repos[name]), "\n"), 3, name)
	}

	// Without the exemption the same expiry empties the stacks.
	main.Git("config", "--unset", "gc.*refs/worktree/stow.reflogExpire")
	main.Git("config", "--unset", "gc.*refs/worktree/stow.reflogExpireUnreachable")
	main.Git("reflog", "expire", "--all")
	for _, name := range c.names {
		require.NotEqual(t, 3, len(strings.Split(stowList(c.repos[name]), "\n")), name)
	}
}

func TestGCProtectionKeepsExistingConfig(t *testing.T) {
	r := gittest.NewRepo(t)
	base(r)
	r.Git("config", "gc.*refs/worktree/stow.reflogExpire", "90.days")
	r.Write("a.txt", "changed\n")
	r.MustStow("push")
	require.Equal(t, "90.days", r.Git("config", "gc.*refs/worktree/stow.reflogExpire"))
	require.Equal(t, "never", r.Git("config", "gc.*refs/worktree/stow.reflogExpireUnreachable"))
}

// Without user.name and user.email, git stash commits under its own fallback
// identity; git stow produces the same commits.
func TestFallbackIdentityMatchesGitStash(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty-gitconfig")
	require.NoError(t, os.WriteFile(empty, nil, 0o644))
	var commits []string
	for _, command := range []string{"stash", "stow"} {
		r := gittest.NewRepo(t)
		base(r)
		r = r.WithEnv("GIT_CONFIG_GLOBAL="+empty, "EMAIL=someone@example.com")
		r.Write("a.txt", "changed\n")
		r.Write("new.txt", "untracked\n")
		res := r.Run("", "", "git", command, "push", "-u")
		require.Equal(t, 0, res.Code, res.Stderr)
		ref := map[string]string{"stash": "refs/stash", "stow": "refs/worktree/stow"}[command]
		require.Equal(t, "git stash <git@stash>", r.Git("log", "-1", "--format=%an <%ae>", ref))
		commits = append(commits, r.Git("rev-parse", ref))
	}
	require.Equal(t, commits[0], commits[1])
}

func TestOutsideARepository(t *testing.T) {
	r := gittest.NewRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(r.Dir, "..", "plain"), 0o755))
	for _, command := range []string{"stash", "stow"} {
		res := r.Run("../plain", "", "git", command, "list")
		require.Equal(t, 128, res.Code, command)
		require.Contains(t, res.Stderr, "fatal: not a git repository", command)
	}
}
