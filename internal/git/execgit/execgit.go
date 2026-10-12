// Package execgit runs the git binary with os/exec.
package execgit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brynbellomy/git-stow/internal/git"
)

// Runner runs the git binary found on PATH. It is the only place git-stow
// starts a process.
type Runner struct {
	binary string
}

var _ git.Runner = (*Runner)(nil)

// New returns a Runner for the git on PATH.
func New() *Runner {
	return &Runner{binary: "git"}
}

// Run implements git.Runner.
func (r *Runner) Run(ctx context.Context, c git.Cmd) (git.Result, error) {
	if !filepath.IsAbs(c.Dir) {
		panic(fmt.Sprintf("execgit: Cmd.Dir must be absolute, got %q (args %q)", c.Dir, c.Args))
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.binary, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = c.Stdin
	cmd.Stdout = &stdout
	if c.Stdout != nil {
		cmd.Stdout = c.Stdout
	}
	cmd.Stderr = &stderr
	if c.Stderr != nil {
		cmd.Stderr = c.Stderr
	}

	err := cmd.Run()
	res := git.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	default:
		return res, fmt.Errorf("running git %s: %w", strings.Join(c.Args, " "), err)
	}
}
