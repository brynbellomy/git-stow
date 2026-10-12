package gitstack

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/brynbellomy/git-stow/internal/stow"
)

// List implements stow.Stack with git stash list's git log invocation,
// renaming the worktree/stow@{n} selectors git prints to stow@{n}.
func (s *Stack) List(ctx context.Context, logArgs []string) error {
	tip, err := s.resolve(ctx, Ref)
	if err != nil || tip == "" {
		return err
	}
	args := append([]string{"log", "--format=%gd: %gs", "-g", "--first-parent"}, logArgs...)
	args = append(args, Ref, "--")
	out := &selectorWriter{w: s.io.Out}
	res, err := s.run(ctx, gitCmd{args: args, passthrough: true, stdout: out})
	if err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return stow.Exit(res.ExitCode)
	}
	return nil
}

// Show implements stow.Stack by delegating to git stash show.
func (s *Stack) Show(ctx context.Context, diffArgs []string, entryArg string) error {
	e, err := s.resolveEntry(ctx, entryArg)
	if err != nil {
		return err
	}
	args := append(append([]string{"stash", "show"}, diffArgs...), e.gitArg())
	return s.passthrough(ctx, gitCmd{args: args})
}

// Apply implements stow.Stack by delegating to git stash apply.
func (s *Stack) Apply(ctx context.Context, o stow.ApplyOptions) error {
	e, err := s.resolveEntry(ctx, o.Entry)
	if err != nil {
		return err
	}
	return s.apply(ctx, e, o)
}

func (s *Stack) apply(ctx context.Context, e entry, o stow.ApplyOptions) error {
	args := []string{"stash", "apply"}
	if o.Index {
		args = append(args, "--index")
	}
	if o.Quiet {
		args = append(args, "--quiet")
	}
	return s.passthrough(ctx, gitCmd{args: append(args, e.gitArg()), stdin: s.io.In})
}

// Pop implements stow.Stack: apply, then drop unless the apply failed.
func (s *Stack) Pop(ctx context.Context, o stow.ApplyOptions) error {
	e, err := s.resolveStowEntry(ctx, o.Entry)
	if err != nil {
		return err
	}
	if err := s.apply(ctx, e, o); err != nil {
		var status *stow.ExitStatus
		if errors.As(err, &status) {
			s.println("The stow entry is kept in case you need it again.")
		}
		return err
	}
	return s.drop(ctx, e, o.Quiet)
}

// Drop implements stow.Stack.
func (s *Stack) Drop(ctx context.Context, o stow.DropOptions) error {
	e, err := s.resolveStowEntry(ctx, o.Entry)
	if err != nil {
		return err
	}
	return s.drop(ctx, e, o.Quiet)
}

// Clear implements stow.Stack.
func (s *Stack) Clear(ctx context.Context) error {
	return s.clear(ctx)
}

// Branch implements stow.Stack by delegating to git stash branch, which does
// not drop anything when handed a commit id, and dropping the entry here.
func (s *Stack) Branch(ctx context.Context, name, entryArg string) error {
	e, err := s.resolveEntry(ctx, entryArg)
	if err != nil {
		return err
	}
	if err := s.passthrough(ctx, gitCmd{args: []string{"stash", "branch", name, e.gitArg()}, stdin: s.io.In}); err != nil {
		return err
	}
	if e.index < 0 {
		return nil
	}
	return s.drop(ctx, e, false)
}

// gitArg is how the entry is handed to git stash: stow entries by object id,
// since git cannot resolve stow@{n}, and other revisions as the user wrote
// them so git's messages name them the same way.
func (e entry) gitArg() string {
	if e.index >= 0 {
		return e.oid
	}
	return e.rev
}

// selectorWriter rewrites the reflog selector git log prints at the start of
// a line, worktree/stow@{n}, to stow@{n}.
type selectorWriter struct {
	w   io.Writer
	buf []byte
}

var (
	gitSelector  = []byte("worktree/stow@{")
	stowSelector = []byte("stow@{")
)

func (sw *selectorWriter) Write(p []byte) (int, error) {
	sw.buf = append(sw.buf, p...)
	for {
		i := bytes.IndexByte(sw.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := sw.emit(sw.buf[:i+1]); err != nil {
			return 0, err
		}
		sw.buf = sw.buf[i+1:]
	}
}

// Close writes a final unterminated line.
func (sw *selectorWriter) Close() error {
	if len(sw.buf) == 0 {
		return nil
	}
	err := sw.emit(sw.buf)
	sw.buf = nil
	return err
}

func (sw *selectorWriter) emit(line []byte) error {
	if rest, ok := bytes.CutPrefix(line, gitSelector); ok {
		line = append(append([]byte{}, stowSelector...), rest...)
	}
	_, err := sw.w.Write(line)
	return err
}
