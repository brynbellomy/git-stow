package gittest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a work tree created by a test under the run's temporary root.
type Repo struct {
	t   testing.TB
	Dir string
	env []string
}

// Result is the outcome of a command a test ran.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// NewRepo creates an empty repository in a fresh t.TempDir().
func NewRepo(t testing.TB) *Repo {
	t.Helper()
	r := &Repo{t: t, Dir: tempDir(t)}
	r.Git("init", "-q")
	return r
}

// tempDir returns a fresh t.TempDir(), resolved, after checking that it lies
// under the run's root, where GIT_CEILING_DIRECTORIES confines discovery.
func tempDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, Root()+string(filepath.Separator)) {
		t.Fatalf("refusing to use %s: not under the test root %s", dir, Root())
	}
	return dir
}

// Path returns the absolute path of a file in the work tree.
func (r *Repo) Path(rel string) string {
	return filepath.Join(r.Dir, filepath.FromSlash(rel))
}

// WithEnv returns a view of the same work tree whose commands also get env.
func (r *Repo) WithEnv(env ...string) *Repo {
	return &Repo{t: r.t, Dir: r.Dir, env: append(append([]string{}, r.env...), env...)}
}

// Run runs a command in a directory relative to the work tree.
func (r *Repo) Run(subdir, stdin, name string, args ...string) Result {
	r.t.Helper()
	res, err := r.TryRun(subdir, stdin, name, args...)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

// TryRun is Run for goroutines other than the test's own: a command that
// cannot be started is reported as an error instead of failing the test.
func (r *Repo) TryRun(subdir, stdin, name string, args ...string) (Result, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = r.Path(subdir)
	cmd.Env = append(os.Environ(), r.env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
	case err != nil:
		return res, fmt.Errorf("running %s %q in %s: %w", name, args, cmd.Dir, err)
	}
	return res, nil
}

// Git runs git at the top of the work tree, fails the test unless it exits 0,
// and returns its stdout without the trailing newline.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	res := r.Run("", "", "git", args...)
	if res.Code != 0 {
		r.t.Fatalf("git %s in %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.Dir, res.Code, res.Stdout, res.Stderr)
	}
	return strings.TrimSuffix(res.Stdout, "\n")
}

// Stow runs `git stow args...` at the top of the work tree.
func (r *Repo) Stow(args ...string) Result {
	r.t.Helper()
	return r.Run("", "", "git", append([]string{"stow"}, args...)...)
}

// MustStow runs `git stow args...` and fails the test unless it exits 0.
func (r *Repo) MustStow(args ...string) Result {
	r.t.Helper()
	res := r.Stow(args...)
	if res.Code != 0 {
		r.t.Fatalf("git stow %s in %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.Dir, res.Code, res.Stdout, res.Stderr)
	}
	return res
}

// Write creates or replaces a file, creating its directories.
func (r *Repo) Write(rel, content string) {
	r.t.Helper()
	p := r.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Read returns a file's content, or "" and false when it does not exist.
func (r *Repo) Read(rel string) (string, bool) {
	r.t.Helper()
	b, err := os.ReadFile(r.Path(rel))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b), true
}

// Remove deletes a file from the work tree.
func (r *Repo) Remove(rel string) {
	r.t.Helper()
	if err := os.Remove(r.Path(rel)); err != nil {
		r.t.Fatal(err)
	}
}

// Commit writes files, stages everything and commits.
func (r *Repo) Commit(msg string, files map[string]string) {
	r.t.Helper()
	for name, content := range files {
		r.Write(name, content)
	}
	r.Git("add", "-A")
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
}

// AddWorktree creates a linked worktree on a new branch in a fresh
// t.TempDir(); the worktree's name is the branch name.
func (r *Repo) AddWorktree(branch string) *Repo {
	r.t.Helper()
	dir := filepath.Join(tempDir(r.t), branch)
	r.Git("worktree", "add", "-q", "-b", branch, dir)
	return &Repo{t: r.t, Dir: dir}
}
