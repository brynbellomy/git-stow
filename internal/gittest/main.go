// Package gittest is the safety harness for tests that run git.
//
// Every package whose tests run git calls Main from its TestMain. Main makes
// the environment hermetic, confines every repository to one temporary root,
// chdirs into a fixture repository holding canaries (a git stash entry, an
// uncommitted change and an untracked file), and fails the run unless the
// canaries are intact afterwards: a test that reaches a repository it did not
// create trips them.
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

// root is the temporary directory that holds every repository of the run.
var root string

// binDir holds the git-stow binary built for the run.
var binDir string

// FixedDate is the author and committer date of every commit tests make, so
// that identical operations produce identical commits.
const FixedDate = "2005-04-07T22:13:13 +0000"

// Main runs the package's tests inside the harness and exits.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return abort(err)
	}

	scrubGitEnv()
	dir, err := os.MkdirTemp("", "git-stow-test-")
	if err != nil {
		return abort(err)
	}
	defer os.RemoveAll(dir)
	if root, err = filepath.EvalSymlinks(dir); err != nil {
		return abort(err)
	}

	// Build before hermeticEnv replaces HOME, which toolchain shims may need.
	binDir = filepath.Join(root, "bin")
	if err := buildBinary(moduleRoot, binDir); err != nil {
		return abort(err)
	}
	if inside, err := insideRepository(root); err != nil || inside {
		return abort(fmt.Errorf("refusing to run: temporary root %s is inside a git repository (err=%v)", root, err))
	}
	if err := hermeticEnv(); err != nil {
		return abort(err)
	}
	os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	fixture := filepath.Join(root, "fixture")
	canaries, err := plantCanaries(fixture)
	if err != nil {
		return abort(fmt.Errorf("planting canaries: %w", err))
	}
	if err := os.Chdir(fixture); err != nil {
		return abort(err)
	}
	if err := checkCwdIsFixture(fixture); err != nil {
		return abort(err)
	}

	code := m.Run()

	if err := canaries.verify(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: canary check: %v\n", err)
		return 1
	}
	return code
}

func abort(err error) int {
	fmt.Fprintf(os.Stderr, "gittest: %v\n", err)
	return 2
}

// Root returns the temporary root that holds every repository of the run.
func Root() string {
	if root == "" {
		panic("gittest: Root called outside gittest.Main")
	}
	return root
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// scrubGitEnv removes every GIT_* variable, so that a GIT_DIR or
// GIT_WORK_TREE inherited from a hook or a shell cannot point a test at a
// real repository.
func scrubGitEnv() {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_") {
			os.Unsetenv(name)
		}
	}
}

// hermeticEnv isolates git from the user's configuration, confines repository
// discovery and temporary directories to root, and pins dates and locale.
func hermeticEnv() error {
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{home, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	config := "[user]\n\tname = Stow Test\n\temail = stow@example.com\n" +
		"[init]\n\tdefaultBranch = main\n" +
		"[commit]\n\tgpgSign = false\n" +
		"[advice]\n\tdetachedHead = false\n"
	if err := os.WriteFile(gitconfig, []byte(config), 0o644); err != nil {
		return err
	}
	env := map[string]string{
		"HOME":                    home,
		"XDG_CONFIG_HOME":         filepath.Join(home, ".config"),
		"TMPDIR":                  tmp,
		"GIT_CONFIG_NOSYSTEM":     "1",
		"GIT_CONFIG_GLOBAL":       gitconfig,
		"GIT_CEILING_DIRECTORIES": root,
		"GIT_AUTHOR_DATE":         FixedDate,
		"GIT_COMMITTER_DATE":      FixedDate,
		"GIT_TERMINAL_PROMPT":     "0",
		"GIT_PAGER":               "cat",
		"PAGER":                   "cat",
		"LC_ALL":                  "C",
		"LANG":                    "C",
		"TZ":                      "UTC",
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func insideRepository(dir string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exitErr):
		return false, nil
	default:
		return false, err
	}
}

func buildBinary(moduleRoot, dir string) error {
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "git-stow"), "./cmd/git-stow")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building git-stow: %v\n%s", err, out)
	}
	return nil
}

// checkCwdIsFixture refuses to run when the process's working directory does
// not resolve to the fixture repository.
func checkCwdIsFixture(fixture string) error {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = fixture
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("refusing to run: fixture is not a repository: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	if top := strings.TrimSpace(string(out)); top != fixture || cwd != fixture {
		return fmt.Errorf("refusing to run: cwd %s / toplevel %s is not the fixture %s", cwd, top, fixture)
	}
	return nil
}

// canaries is the fixture state no test may change.
type canaries struct {
	dir       string
	stashOID  string
	status    string
	tracked   string
	untracked string
}

func plantCanaries(dir string) (*canaries, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	git := func(args ...string) (string, error) { return gitIn(dir, args...) }
	write := func(name, content string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}
	steps := []func() error{
		func() error { _, err := git("init", "-q"); return err },
		func() error { return write("tracked.txt", "committed\n") },
		func() error { _, err := git("add", "tracked.txt"); return err },
		func() error { _, err := git("commit", "-q", "-m", "canary base"); return err },
		func() error { return write("tracked.txt", "stashed\n") },
		func() error { _, err := git("stash", "push", "-q", "-m", "canary stash"); return err },
		func() error { return write("tracked.txt", "uncommitted\n") },
		func() error { return write("untracked.txt", "untracked\n") },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	c := &canaries{dir: dir, tracked: "uncommitted\n", untracked: "untracked\n"}
	var err error
	if c.stashOID, err = git("rev-parse", "refs/stash"); err != nil {
		return nil, err
	}
	if c.status, err = git("status", "--porcelain", "--untracked-files=all"); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *canaries) verify() error {
	var problems []string
	check := func(what, got, want string) {
		if got != want {
			problems = append(problems, fmt.Sprintf("%s: got %q, want %q", what, got, want))
		}
	}
	stashes, err := gitIn(c.dir, "log", "-g", "--format=%H", "refs/stash", "--")
	if err != nil {
		problems = append(problems, "reading stash list: "+err.Error())
	}
	check("stash list", stashes, c.stashOID)
	status, err := gitIn(c.dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		problems = append(problems, "reading status: "+err.Error())
	}
	check("status", status, c.status)
	tracked, _ := os.ReadFile(filepath.Join(c.dir, "tracked.txt"))
	check("tracked.txt", string(tracked), c.tracked)
	untracked, _ := os.ReadFile(filepath.Join(c.dir, "untracked.txt"))
	check("untracked.txt", string(untracked), c.untracked)
	if refs, _ := gitIn(c.dir, "for-each-ref", "refs/worktree/"); refs != "" {
		problems = append(problems, "unexpected per-worktree refs: "+refs)
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// gitIn runs git in dir and returns its trimmed stdout.
func gitIn(dir string, args ...string) (string, error) {
	if !filepath.IsAbs(dir) {
		panic(fmt.Sprintf("gittest: git %q needs an absolute directory, got %q", args, dir))
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}
