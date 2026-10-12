package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brynbellomy/git-stow/internal/gittest"
)

// state is everything about a repository that a stash operation may change.
type state struct {
	Head     string
	Branch   string
	Branches string
	Index    string
	Status   string
	Files    map[string]string
	Entries  []entryState
}

// entryState describes one stash or stow entry.
type entryState struct {
	Commit    string
	Tree      string
	Parents   string
	Message   string
	Reflog    string
	IndexTree string
	UTree     string
}

// snapshot captures the state of r; ref is refs/stash or refs/worktree/stow.
func snapshot(t *testing.T, r *gittest.Repo, ref string) state {
	t.Helper()
	optional := func(args ...string) string {
		res := r.Run("", "", "git", args...)
		if res.Code != 0 {
			return "<none>"
		}
		return strings.TrimSpace(res.Stdout)
	}
	s := state{
		Head:     optional("rev-parse", "--verify", "-q", "HEAD"),
		Branch:   optional("symbolic-ref", "-q", "HEAD"),
		Branches: r.Git("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"),
		Index:    r.Git("ls-files", "--stage"),
		Status:   r.Git("status", "--porcelain=v1", "--untracked-files=all", "--ignored"),
		Files:    files(t, r.Dir),
	}
	if optional("rev-parse", "--verify", "-q", ref) == "<none>" {
		return s
	}
	for _, record := range strings.Split(r.Git("log", "-g", "--format=%H%x00%T%x00%P%x00%B%x00%gs%x1e", ref, "--"), "\x1e") {
		if record = strings.TrimSpace(record); record == "" {
			continue
		}
		f := strings.Split(record, "\x00")
		e := entryState{Commit: f[0], Tree: f[1], Parents: f[2], Message: f[3], Reflog: f[4]}
		e.IndexTree = optional("rev-parse", "--verify", "-q", e.Commit+"^2^{tree}")
		e.UTree = optional("rev-parse", "--verify", "-q", e.Commit+"^3^{tree}")
		s.Entries = append(s.Entries, e)
	}
	return s
}

// files maps each path in the work tree, outside .git, to its type, mode and
// content digest.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "link " + target
		case info.IsDir():
			if rel != "." {
				out[rel+"/"] = "dir"
			}
		default:
			content, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			out[rel] = info.Mode().Perm().String() + " " + hex.EncodeToString(sum[:8])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
