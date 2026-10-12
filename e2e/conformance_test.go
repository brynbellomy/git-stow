package e2e

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brynbellomy/git-stow/internal/gittest"
)

// The conformance suite runs every scenario twice from identical starting
// repositories: with git stash in repository A and with git stow in
// repository B. After each step the exit status, the output, the work tree,
// the index, HEAD, the branches and every entry's commits must match. Commit
// dates and identities are pinned, so matching entries are byte-identical
// commits with identical object ids.

// step is one stash operation, run as `git stash <args>` and `git stow <args>`.
type step struct {
	// args may hold placeholders: {n} is entry n (stash@{n} or stow@{n})
	// and {oid0} is the object id of entry 0.
	args []string
	// dir is the subdirectory the command runs in.
	dir   string
	stdin string
	// before mutates both repositories identically before the command runs.
	before func(r *gittest.Repo)
	// outputDiffers names why the command's output is not compared.
	outputDiffers string
}

type scenario struct {
	name  string
	setup func(r *gittest.Repo)
	steps []step
}

// stashToStow rewrites git stash's wording to git stow's. These are the only
// output differences between the two commands, and each follows from the
// entries being named stow@{n}.
var stashToStow = strings.NewReplacer(
	"refs/stash@{", "stow@{",
	"stash@{", "stow@{",
	"No stash entries found.", "No stow entries found.",
	"is not a stash reference", "is not a stow reference",
	"The stash entry is kept in case you need it again.", "The stow entry is kept in case you need it again.",
	"Could not drop stash entry", "Could not drop stow entry",
	"git stash clear with arguments", "git stow clear with arguments",
	"log for 'refs/stash'", "log for 'stow'",
	"log for 'stash'", "log for 'stow'",
)

func push(args ...string) step { return step{args: append([]string{"push"}, args...)} }
func cmd(args ...string) step  { return step{args: args} }

// base commits the files most scenarios start from.
func base(r *gittest.Repo) {
	r.Commit("base", map[string]string{
		"a.txt":      "a\n",
		"b.txt":      "b\n",
		"dir/c.txt":  "c\n",
		"bin.dat":    "\x00\x01\x02binary\x00",
		"long.txt":   numbered(20),
		".gitignore": "*.log\n",
	})
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func with(fns ...func(r *gittest.Repo)) func(r *gittest.Repo) {
	return func(r *gittest.Repo) {
		for _, fn := range fns {
			fn(r)
		}
	}
}

func write(rel, content string) func(r *gittest.Repo) {
	return func(r *gittest.Repo) { r.Write(rel, content) }
}

func git(args ...string) func(r *gittest.Repo) {
	return func(r *gittest.Repo) { r.Git(args...) }
}

func remove(rel string) func(r *gittest.Repo) {
	return func(r *gittest.Repo) { r.Remove(rel) }
}

func chmod(rel string, mode os.FileMode) func(r *gittest.Repo) {
	return func(r *gittest.Repo) {
		if err := os.Chmod(r.Path(rel), mode); err != nil {
			panic(err)
		}
	}
}

func symlink(target, rel string) func(r *gittest.Repo) {
	return func(r *gittest.Repo) {
		if err := os.Symlink(target, r.Path(rel)); err != nil {
			panic(err)
		}
	}
}

var scenarios = []scenario{
	{
		name:  "tracked edits",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push(), cmd("list"), cmd("show"), cmd("show", "-p"), cmd("pop")},
	},
	{
		name:  "bare command is push",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{cmd(), cmd("list"), cmd("pop")},
	},
	{
		name:  "staged edits",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt")),
		steps: []step{push(), cmd("pop", "--index")},
	},
	{
		name:  "staged edits restored without --index",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt")),
		steps: []step{push(), cmd("pop")},
	},
	{
		name:  "staged and unstaged edits of one file",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt"), write("a.txt", "a unstaged\n")),
		steps: []step{push(), cmd("apply", "--index"), cmd("drop")},
	},
	{
		name:  "untracked files stay without -u",
		setup: with(base, write("a.txt", "a changed\n"), write("new.txt", "new\n")),
		steps: []step{push(), cmd("pop")},
	},
	{
		name: "include untracked",
		setup: with(base, write("a.txt", "a changed\n"), write("new.txt", "new\n"),
			write("newdir/deep/n.txt", "n\n"), write("x.log", "ignored\n")),
		steps: []step{push("-u"), cmd("show", "--include-untracked"), cmd("show", "--only-untracked"), cmd("pop")},
	},
	{
		name:  "untracked files only",
		setup: with(base, write("new.txt", "new\n")),
		steps: []step{push("--include-untracked"), cmd("list"), cmd("pop")},
	},
	{
		name:  "all includes ignored files",
		setup: with(base, write("new.txt", "new\n"), write("x.log", "ignored\n"), write("logs/y.log", "y\n")),
		steps: []step{push("-a"), cmd("pop")},
	},
	{
		name:  "keep index",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt"), write("b.txt", "b unstaged\n")),
		steps: []step{push("--keep-index"), cmd("pop")},
	},
	{
		name:  "keep index short flag with untracked",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt"), write("b.txt", "b unstaged\n"), write("new.txt", "n\n")),
		steps: []step{push("-k", "-u"), cmd("pop", "--index")},
	},
	{
		name:  "pathspec",
		setup: with(base, write("a.txt", "a changed\n"), write("b.txt", "b changed\n")),
		steps: []step{push("--", "a.txt"), cmd("list"), cmd("show", "-p"), cmd("pop")},
	},
	{
		name:  "pathspec without dashdash",
		setup: with(base, write("a.txt", "a changed\n"), write("b.txt", "b changed\n")),
		steps: []step{push("a.txt"), cmd("pop")},
	},
	{
		name: "pathspec with staged changes outside it",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt"), write("b.txt", "b changed\n"),
			write("dir/c.txt", "c changed\n")),
		steps: []step{push("--", "b.txt"), cmd("pop", "--index")},
	},
	{
		name:  "pathspec with untracked files",
		setup: with(base, write("a.txt", "a changed\n"), write("new.txt", "new\n"), write("other.txt", "other\n")),
		steps: []step{push("-u", "--", "new.txt"), cmd("show", "--include-untracked"), cmd("pop")},
	},
	{
		name:  "glob pathspec",
		setup: with(base, write("a.txt", "a changed\n"), write("dir/c.txt", "c changed\n")),
		steps: []step{push("--", "dir/*"), cmd("pop")},
	},
	{
		name:  "pathspec matching nothing",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push("--", "nope.txt"), cmd("list")},
	},
	{
		name:  "pathspec with keep index",
		setup: with(base, write("a.txt", "a staged\n"), git("add", "a.txt"), write("a.txt", "a unstaged\n"), write("b.txt", "b changed\n")),
		steps: []step{push("-k", "--", "a.txt"), cmd("pop")},
	},
	{
		name:  "relative pathspec from a subdirectory",
		setup: with(base, write("a.txt", "a changed\n"), write("dir/c.txt", "c changed\n")),
		steps: []step{
			{args: []string{"push", "--", "c.txt"}, dir: "dir"},
			{args: []string{"list"}, dir: "dir"},
			{args: []string{"pop"}, dir: "dir"},
		},
	},
	{
		name:  "push from a subdirectory",
		setup: with(base, write("a.txt", "a changed\n"), write("dir/c.txt", "c changed\n"), write("dir/new.txt", "n\n"), write("new.txt", "n\n")),
		steps: []step{{args: []string{"push", "-u"}, dir: "dir"}, {args: []string{"pop"}, dir: "dir"}},
	},
	{
		name:  "rename",
		setup: with(base, git("mv", "a.txt", "renamed.txt")),
		steps: []step{push(), cmd("show"), cmd("pop", "--index")},
	},
	{
		name:  "deletions",
		setup: with(base, remove("b.txt"), git("rm", "-q", "dir/c.txt")),
		steps: []step{push(), cmd("pop")},
	},
	{
		name:  "binary file",
		setup: with(base, write("bin.dat", "\x00\xff\xfechanged\x00")),
		steps: []step{push(), cmd("show", "--stat"), cmd("pop")},
	},
	{
		name:  "mode change",
		setup: with(base, chmod("a.txt", 0o755)),
		steps: []step{push(), cmd("pop")},
	},
	{
		name:  "file becomes symlink",
		setup: with(base, remove("a.txt"), symlink("b.txt", "a.txt")),
		steps: []step{push(), cmd("pop")},
	},
	{
		name:  "message",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push("-m", "my message"), push("--message=ignored"), cmd("list"), cmd("pop")},
	},
	{
		name:  "detached HEAD",
		setup: with(base, git("checkout", "-q", "--detach"), write("a.txt", "a changed\n")),
		steps: []step{push(), cmd("list"), cmd("pop")},
	},
	{
		name:  "no local changes",
		setup: base,
		steps: []step{push(), cmd("list"), cmd("show"), cmd("pop"), cmd("drop"), cmd("clear")},
	},
	{
		name:  "no initial commit",
		setup: write("a.txt", "a\n"),
		steps: []step{push(), cmd("list")},
	},
	{
		name:  "quiet",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push("-q"), cmd("pop", "-q"), push("--quiet"), cmd("drop", "-q")},
	},
	{
		name:  "stack of entries",
		setup: with(base, write("a.txt", "a one\n")),
		steps: []step{
			push(),
			{args: []string{"push", "-m", "second"}, before: write("b.txt", "b two\n")},
			{args: []string{"push"}, before: write("a.txt", "a three\n")},
			cmd("list"),
			cmd("show", "{1}"),
			cmd("show", "-p", "2"),
			cmd("drop", "{1}"),
			cmd("list"),
			cmd("pop", "{1}"),
			cmd("list"),
			cmd("apply", "0"),
			cmd("clear"),
			cmd("list"),
		},
	},
	{
		name:  "apply by object id",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push(), cmd("apply", "{oid0}"), cmd("show", "{oid0}"), cmd("list")},
	},
	{
		name:  "invalid entries",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{
			push(),
			cmd("drop", "{5}"),
			cmd("pop", "7"),
			cmd("apply", "{3}"),
			cmd("show", "nonexistent"),
			cmd("drop", "HEAD"),
			cmd("drop", "{oid0}"),
			cmd("apply", "HEAD"),
			cmd("pop", "{0}", "{1}"),
			cmd("list"),
		},
	},
	{
		name:  "clear with arguments",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push(), cmd("clear", "extra"), cmd("list")},
	},
	{
		name:  "branch",
		setup: with(base, write("a.txt", "a one\n")),
		steps: []step{
			push(),
			{args: []string{"push"}, before: with(write("b.txt", "b two\n"), git("add", "b.txt"))},
			{args: []string{"branch", "newbranch", "{1}"}, before: git("commit", "-q", "--allow-empty", "-m", "moved on")},
			cmd("list"),
			cmd("branch", "otherbranch"),
			cmd("list"),
		},
	},
	{
		name:  "branch that already exists",
		setup: with(base, write("a.txt", "a one\n"), git("branch", "taken")),
		steps: []step{push(), cmd("branch", "taken"), cmd("list")},
	},
	{
		name:  "conflict on apply",
		setup: with(base, write("a.txt", "stashed\n")),
		steps: []step{
			push(),
			{args: []string{"apply"}, before: git("commit", "-q", "--allow-empty", "-m", "noop")},
			cmd("list"),
		},
	},
	{
		name:  "pop with a conflict keeps the entry",
		setup: with(base, write("a.txt", "stashed\n")),
		steps: []step{
			push(),
			{args: []string{"pop"}, before: with(write("a.txt", "committed\n"), git("commit", "-q", "-am", "conflicting"))},
			cmd("list"),
		},
	},
	{
		name:  "apply onto local changes that would be overwritten",
		setup: with(base, write("a.txt", "stashed\n")),
		steps: []step{push(), {args: []string{"pop"}, before: write("a.txt", "local\n")}, cmd("list")},
	},
	{
		name:  "pop untracked files that already exist",
		setup: with(base, write("new.txt", "stashed\n")),
		steps: []step{push("-u"), {args: []string{"pop"}, before: write("new.txt", "in the way\n")}, cmd("list")},
	},
	{
		name:  "patch mode",
		setup: with(base, write("long.txt", strings.Replace(strings.Replace(numbered(20), "line 1\n", "LINE 1\n", 1), "line 20\n", "LINE 20\n", 1))),
		steps: []step{
			{args: []string{"push", "-p"}, stdin: "y\nn\n", outputDiffers: "git add -p asks \"Stage this hunk\" where git stash -p asks \"Stash this hunk\""},
			cmd("show", "-p"),
			cmd("pop"),
		},
	},
	{
		name:  "push with a pathspec cannot be assumed even after -p",
		setup: with(base, write("a.txt", "a changed\n"), write("b.txt", "b changed\n")),
		steps: []step{cmd("-p", "a.txt"), {args: []string{"-p", "--", "a.txt"}, stdin: "y\n", outputDiffers: "prompt wording"}, cmd("list")},
	},
	{
		name:  "patch mode with nothing selected",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{{args: []string{"push", "-p"}, stdin: "n\n", outputDiffers: "prompt wording"}, cmd("list")},
	},
	{
		name:  "patch mode refuses untracked",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{push("-p", "-u")},
	},
	{
		name:  "push cannot be assumed",
		setup: with(base, write("a.txt", "a changed\n")),
		steps: []step{cmd("-u", "a.txt"), cmd("list")},
	},
}

func TestConformance(t *testing.T) {
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			stash, stow := gittest.NewRepo(t), gittest.NewRepo(t)
			sc.setup(stash)
			sc.setup(stow)
			require.Equal(t, snapshot(t, stash, "refs/stash"), snapshot(t, stow, "refs/worktree/stow"), "setup diverged")

			for i, st := range sc.steps {
				if st.before != nil {
					st.before(stash)
					st.before(stow)
				}
				label := fmt.Sprintf("step %d: %q", i, st.args)
				want := stash.Run(st.dir, st.stdin, "git", append([]string{"stash"}, expand(t, stash, "refs/stash", "stash", st.args)...)...)
				got := stow.Run(st.dir, st.stdin, "git", append([]string{"stow"}, expand(t, stow, "refs/worktree/stow", "stow", st.args)...)...)

				require.Equal(t, want.Code, got.Code, "%s exit status\nstash stderr: %s\nstow stderr: %s", label, want.Stderr, got.Stderr)
				if st.outputDiffers == "" {
					require.Equal(t, normalize(stash, want.Stdout), normalize(stow, got.Stdout), "%s stdout", label)
					require.Equal(t, normalize(stash, want.Stderr), normalize(stow, got.Stderr), "%s stderr", label)
				}
				require.Equal(t, snapshot(t, stash, "refs/stash"), snapshot(t, stow, "refs/worktree/stow"), "%s state", label)
			}
		})
	}
}

// expand substitutes the step placeholders for one side of the comparison.
func expand(t *testing.T, r *gittest.Repo, ref, name string, args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case a == "{oid0}":
			a = r.Git("rev-parse", ref)
		case strings.HasPrefix(a, "{") && strings.HasSuffix(a, "}"):
			a = name + "@" + a
		}
		out[i] = a
	}
	return out
}

// normalize maps git stash's wording to git stow's and hides the repository's
// location.
func normalize(r *gittest.Repo, s string) string {
	s = prefixMagic.ReplaceAllString(s, "")
	return stashToStow.Replace(strings.ReplaceAll(s, r.Dir, "<repo>"))
}

// prefixMagic is the :(prefix:<n>) git stash leaks into its "pathspec did not
// match" message; git stow reports the pathspec as typed.
var prefixMagic = regexp.MustCompile(`:\(prefix:[0-9]+\)`)
