package e2e

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brynbellomy/git-stow/internal/gittest"
)

// clone is a main checkout with two linked worktrees, keyed by name.
type clone struct {
	names []string
	repos map[string]*gittest.Repo
	// branch is each worktree's branch.
	branch map[string]string
}

func newClone(t *testing.T) *clone {
	t.Helper()
	main := gittest.NewRepo(t)
	base(main)
	return &clone{
		names:  []string{"main", "one", "two"},
		repos:  map[string]*gittest.Repo{"main": main, "one": main.AddWorktree("one"), "two": main.AddWorktree("two")},
		branch: map[string]string{"main": "main", "one": "one", "two": "two"},
	}
}

func stowList(r *gittest.Repo) string {
	return strings.TrimSpace(r.MustStow("list").Stdout)
}

func TestIsolationBetweenWorktrees(t *testing.T) {
	c := newClone(t)
	main := c.repos["main"]

	main.Write("a.txt", "shared stash\n")
	main.Git("stash", "push", "-q", "-m", "shared")
	sharedStash := main.Git("rev-parse", "refs/stash")
	sharedList := main.Git("stash", "list")

	for _, name := range c.names {
		r := c.repos[name]
		r.Write("a.txt", name+"\n")
		r.MustStow("push", "-m", "from "+name)
	}
	for _, name := range c.names {
		r := c.repos[name]
		require.Equal(t, fmt.Sprintf("stow@{0}: On %s: from %s", c.branch[name], name), stowList(r), name)
		require.Equal(t, "a\n", mustRead(t, r, "a.txt"), "%s: push reset the work tree", name)
	}

	// Popping in one worktree restores its own change and leaves the others.
	c.repos["one"].MustStow("pop")
	require.Equal(t, "one\n", mustRead(t, c.repos["one"], "a.txt"))
	require.Empty(t, stowList(c.repos["one"]))
	require.Equal(t, "stow@{0}: On main: from main", stowList(main))
	require.Equal(t, "stow@{0}: On two: from two", stowList(c.repos["two"]))

	// Clearing one worktree's stack leaves the others.
	c.repos["two"].MustStow("clear")
	require.Empty(t, stowList(c.repos["two"]))
	require.Equal(t, "stow@{0}: On main: from main", stowList(main))

	// Errors in an empty worktree do not reach another worktree's entries.
	res := c.repos["two"].Stow("drop")
	require.Equal(t, 1, res.Code)
	require.Equal(t, "No stow entries found.\n", res.Stderr)
	require.Equal(t, "stow@{0}: On main: from main", stowList(main))

	// The shared stash saw none of it.
	for _, name := range c.names {
		require.Equal(t, sharedStash, c.repos[name].Git("rev-parse", "refs/stash"), name)
		require.Equal(t, sharedList, c.repos[name].Git("stash", "list"), name)
	}

	// Nor does git stash reach stow entries: clearing the shared stash from
	// any worktree leaves every stow stack alone.
	c.repos["one"].Git("stash", "clear")
	require.Equal(t, "stow@{0}: On main: from main", stowList(main))
}

func mustRead(t *testing.T, r *gittest.Repo, rel string) string {
	t.Helper()
	content, ok := r.Read(rel)
	require.True(t, ok, "%s does not exist", rel)
	return content
}

func TestConcurrentPushesInDifferentWorktrees(t *testing.T) {
	main := gittest.NewRepo(t)
	base(main)
	repos := []*gittest.Repo{main}
	for i := 1; i < 8; i++ {
		repos = append(repos, main.AddWorktree(fmt.Sprintf("wt%d", i)))
	}

	const rounds = 3
	for round := 0; round < rounds; round++ {
		for i, r := range repos {
			r.Write("a.txt", fmt.Sprintf("worktree %d round %d\n", i, round))
			r.Write(fmt.Sprintf("new-%d.txt", round), "untracked\n")
		}
		results := make([]gittest.Result, len(repos))
		errs := make([]error, len(repos))
		var wg sync.WaitGroup
		for i, r := range repos {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], errs[i] = r.TryRun("", "", "git", "stow", "push", "-u", "-m", fmt.Sprintf("worktree %d round %d", i, round))
			}()
		}
		wg.Wait()
		for i := range repos {
			require.NoError(t, errs[i])
			require.Equal(t, 0, results[i].Code, "worktree %d round %d: %s", i, round, results[i].Stderr)
		}
	}

	for i, r := range repos {
		var want []string
		for round := rounds - 1; round >= 0; round-- {
			want = append(want, fmt.Sprintf("worktree %d round %d", i, round))
		}
		var got []string
		for _, line := range strings.Split(stowList(r), "\n") {
			_, msg, _ := strings.Cut(line, ": On ")
			_, msg, _ = strings.Cut(msg, ": ")
			got = append(got, msg)
		}
		require.Equal(t, want, got, "worktree %d", i)
		for n := 0; n < rounds; n++ {
			round := rounds - 1 - n
			rev := fmt.Sprintf("refs/worktree/stow@{%d}", n)
			require.Equal(t, fmt.Sprintf("worktree %d round %d", i, round), r.Git("show", rev+":a.txt"))
			require.Equal(t, "untracked", r.Git("show", fmt.Sprintf("%s^3:new-%d.txt", rev, round)))
		}
	}
}

// Concurrent pushes in one worktree race for its index and work tree; git
// stash makes no promise about the outcome either. What git stow promises is
// that every push that reports an entry saved finds it on the stack.
func TestConcurrentPushesInOneWorktreeNeverLoseAnEntry(t *testing.T) {
	r := gittest.NewRepo(t)
	base(r)
	r.Write("a.txt", "contended\n")

	const pushers = 8
	results := make([]gittest.Result, pushers)
	errs := make([]error, pushers)
	var wg sync.WaitGroup
	for i := 0; i < pushers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = r.TryRun("", "", "git", "stow", "push", "-m", fmt.Sprintf("pusher %d", i))
		}()
	}
	wg.Wait()

	list := stowList(r)
	saved := 0
	for i := 0; i < pushers; i++ {
		require.NoError(t, errs[i])
		if strings.Contains(results[i].Stdout, "Saved working directory") {
			saved++
			require.Contains(t, list, fmt.Sprintf(": pusher %d", i))
		}
	}
	t.Logf("%d of %d concurrent pushes saved an entry", saved, pushers)
	require.GreaterOrEqual(t, saved, 1)
	require.Len(t, strings.Split(list, "\n"), saved, list)
}
