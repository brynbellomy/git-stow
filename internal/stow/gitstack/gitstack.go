// Package gitstack implements stow.Stack by driving the git binary.
//
// Entries live in the per-worktree ref refs/worktree/stow and its reflog,
// exactly as git stash keeps refs/stash. Whatever git stash exposes (stash
// create, stash apply, stash show, stash branch) is delegated to it; the rest
// mirrors builtin/stash.c step for step with git plumbing.
package gitstack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/brynbellomy/git-stow/internal/git"
	"github.com/brynbellomy/git-stow/internal/stow"
)

// Ref is the per-worktree ref whose reflog holds the stow entries.
const Ref = "refs/worktree/stow"

// IO holds the streams a Stack reads from and writes to.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Stack is the stow stack of the worktree containing a directory.
type Stack struct {
	git git.Runner
	io  IO
	cwd string // the directory the user ran git-stow in
	top string // the top of its work tree
}

var _ stow.Stack = (*Stack)(nil)

// New returns the Stack of the worktree containing cwd, which must be absolute.
// When cwd is not inside a work tree, git's message is printed and the error
// is an *stow.ExitStatus.
func New(ctx context.Context, runner git.Runner, cwd string, streams IO) (*Stack, error) {
	if runner == nil {
		panic("gitstack: nil runner")
	}
	s := &Stack{git: runner, io: streams, cwd: cwd}
	res, err := s.run(ctx, gitCmd{args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		s.io.Err.Write(res.Stderr)
		return nil, stow.Exit(128)
	}
	s.top = strings.TrimSuffix(string(res.Stdout), "\n")
	return s, nil
}

// gitCmd is one git invocation relative to the Stack's directories.
type gitCmd struct {
	args  []string
	atTop bool // run at the top of the work tree instead of cwd
	env   []string
	stdin io.Reader
	// passthrough connects stdout and stderr to the user's streams.
	passthrough bool
	// stdout, when set, receives stdout instead.
	stdout io.Writer
}

func (s *Stack) run(ctx context.Context, c gitCmd) (git.Result, error) {
	cmd := git.Cmd{Dir: s.cwd, Args: c.args, Env: c.env, Stdin: c.stdin}
	if c.atTop {
		cmd.Dir = s.top
	}
	if c.passthrough {
		cmd.Stdout = s.io.Out
		cmd.Stderr = s.io.Err
	}
	if c.stdout != nil {
		cmd.Stdout = c.stdout
	}
	return s.git.Run(ctx, cmd)
}

// exec runs c and fails unless git exits 0; its stdout is returned without
// the trailing newline.
func (s *Stack) exec(ctx context.Context, c gitCmd) (string, error) {
	res, err := s.run(ctx, c)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git %s: exit status %d: %s", strings.Join(c.args, " "), res.ExitCode, bytes.TrimSpace(res.Stderr))
	}
	return strings.TrimSuffix(string(res.Stdout), "\n"), nil
}

// gitOut runs `git args...` in cwd; see exec.
func (s *Stack) gitOut(ctx context.Context, args ...string) (string, error) {
	return s.exec(ctx, gitCmd{args: args})
}

// check runs a command whose exit status is a yes/no answer: 0 is true, 1 is
// false, anything else is an error.
func (s *Stack) check(ctx context.Context, args ...string) (bool, error) {
	res, err := s.run(ctx, gitCmd{args: args})
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("git %s: exit status %d: %s", strings.Join(args, " "), res.ExitCode, bytes.TrimSpace(res.Stderr))
	}
}

// passthrough runs a command wired to the user's streams and converts a
// non-zero exit into an *stow.ExitStatus carrying git's code.
func (s *Stack) passthrough(ctx context.Context, c gitCmd) error {
	c.passthrough = true
	res, err := s.run(ctx, c)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return stow.Exit(res.ExitCode)
	}
	return nil
}

// step runs one step of a multi-step command with passthrough. As in git
// stash, a failed step fails the command with exit status 1.
func (s *Stack) step(ctx context.Context, c gitCmd) error {
	err := s.passthrough(ctx, c)
	var status *stow.ExitStatus
	if errors.As(err, &status) {
		return stow.Exit(1)
	}
	return err
}

// resolve returns the object a revision names, or "" when it names none.
func (s *Stack) resolve(ctx context.Context, rev string) (string, error) {
	res, err := s.run(ctx, gitCmd{args: []string{"rev-parse", "--quiet", "--verify", rev}})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (s *Stack) println(a ...any) {
	fmt.Fprintln(s.io.Out, a...)
}

func (s *Stack) eprintln(a ...any) {
	fmt.Fprintln(s.io.Err, a...)
}

// errorf prints a message the way git's error() does and returns exit 1.
func (s *Stack) errorf(format string, a ...any) error {
	fmt.Fprintf(s.io.Err, "error: "+format+"\n", a...)
	return stow.Exit(1)
}

// entry is a resolved entry argument.
type entry struct {
	name  string // how messages refer to it: stow@{n} or the user's revision
	rev   string // the revision handed to git
	index int    // position in the stack, or -1 when it is not a stow entry
	oid   string
}

var entryPattern = regexp.MustCompile(`^stow@\{([0-9]+)\}$`)

// resolveEntry interprets an entry argument as git stash's get_stash_info
// does: empty means stow@{0}, a bare number n means stow@{n}, and anything
// else is a revision.
func (s *Stack) resolveEntry(ctx context.Context, arg string) (entry, error) {
	if arg == "" {
		top, err := s.resolve(ctx, Ref)
		if err != nil {
			return entry{}, err
		}
		if top == "" {
			s.eprintln("No stow entries found.")
			return entry{}, stow.Exit(1)
		}
		arg = "stow@{0}"
	} else if strings.Trim(arg, "0123456789") == "" {
		arg = "stow@{" + arg + "}"
	}

	e := entry{name: arg, rev: arg, index: -1}
	if m := entryPattern.FindStringSubmatch(arg); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return entry{}, s.errorf("%s is not a valid reference", arg)
		}
		e.index = n
		e.rev = fmt.Sprintf("%s@{%d}", Ref, n)
	}

	res, err := s.run(ctx, gitCmd{args: []string{"rev-parse", "--verify", e.rev}})
	if err != nil {
		return entry{}, err
	}
	if res.ExitCode != 0 {
		// Past the end of the reflog git dies with "log for '<ref>' only has
		// <n> entries"; git stash passes that through.
		if msg := string(res.Stderr); e.index >= 0 && strings.HasPrefix(msg, "fatal: log for '"+Ref+"'") {
			fmt.Fprint(s.io.Err, strings.Replace(msg, "'"+Ref+"'", "'stow'", 1))
			return entry{}, stow.Exit(128)
		}
		return entry{}, s.errorf("%s is not a valid reference", arg)
	}
	oid := strings.TrimSpace(string(res.Stdout))
	// git stash's assert_stash_like.
	for _, part := range []string{"^1", ":", "^1:", "^2:"} {
		if p, err := s.resolve(ctx, e.rev+part); err != nil || p == "" {
			if err != nil {
				return entry{}, err
			}
			fmt.Fprintf(s.io.Err, "fatal: '%s' is not a stash-like commit\n", arg)
			return entry{}, stow.Exit(128)
		}
	}
	e.oid = oid
	return e, nil
}

// resolveStowEntry is resolveEntry for commands that only accept entries of
// this worktree's stack.
func (s *Stack) resolveStowEntry(ctx context.Context, arg string) (entry, error) {
	e, err := s.resolveEntry(ctx, arg)
	if err != nil {
		return entry{}, err
	}
	if e.index < 0 {
		return entry{}, s.errorf("'%s' is not a stow reference", e.name)
	}
	return e, nil
}

// tempIndexPath returns a private index file next to the worktree's index, as
// git stash's index.stash.<pid>.
func (s *Stack) tempIndexPath(ctx context.Context) (string, error) {
	p, err := s.gitOut(ctx, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.cwd, p)
	}
	return fmt.Sprintf("%s.stow.%d", p, os.Getpid()), nil
}
