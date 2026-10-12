package gitstack

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/brynbellomy/git-stow/internal/stow"
)

// Push implements stow.Stack. It follows do_push_stash in git's
// builtin/stash.c: validate, record the entry, store it, then reset what it
// recorded.
func (s *Stack) Push(ctx context.Context, o stow.PushOptions) error {
	keep := o.KeepIndex
	if o.Patch && keep == stow.KeepIndexUnset {
		keep = stow.KeepIndexOn
	}
	if o.Patch && o.Untracked != stow.UntrackedNone {
		s.eprintln("Can't use --patch and --include-untracked or --all at the same time")
		return stow.Exit(1)
	}

	if o.Untracked == stow.UntrackedNone && len(o.Pathspecs) > 0 {
		args := append([]string{"ls-files", "--error-unmatch", "--"}, o.Pathspecs...)
		res, err := s.run(ctx, gitCmd{args: args})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			// ls-files prints git stash's message, "Did you forget" hint included.
			s.io.Err.Write(res.Stderr)
			return stow.Exit(1)
		}
	}

	if _, err := s.gitOut(ctx, "update-index", "-q", "--refresh"); err != nil {
		return s.errorf("could not write index")
	}

	head, err := s.resolve(ctx, "HEAD^{commit}")
	if err != nil {
		return err
	}
	if head == "" {
		if !o.Quiet {
			s.eprintln("You do not have the initial commit yet")
		}
		return stow.Exit(1)
	}

	tracked, err := s.trackedChanges(ctx, o.Pathspecs)
	if err != nil {
		return err
	}
	var untracked []byte
	if o.Untracked != stow.UntrackedNone {
		if untracked, err = s.untrackedFiles(ctx, o.Untracked, o.Pathspecs); err != nil {
			return err
		}
	}
	if !tracked && len(untracked) == 0 {
		if !o.Quiet {
			s.println("No local changes to save")
		}
		return nil
	}

	if err := s.ensureGCProtection(ctx); err != nil {
		return err
	}

	rec, err := s.record(ctx, head, o, untracked)
	if err != nil {
		return err
	}
	if err := s.store(ctx, rec.commit, rec.msg); err != nil {
		return err
	}
	if !o.Quiet {
		s.println("Saved working directory and index state " + rec.msg)
	}

	if o.Patch {
		return s.resetPatch(ctx, rec, keep, o.Pathspecs)
	}
	return s.reset(ctx, rec, keep, o)
}

// trackedChanges reports whether the index or the work tree differ from HEAD
// within pathspecs, ignoring submodules as git stash does.
func (s *Stack) trackedChanges(ctx context.Context, pathspecs []string) (bool, error) {
	same, err := s.check(ctx, append([]string{"diff-index", "--cached", "--quiet", "--ignore-submodules", "HEAD", "--"}, pathspecs...)...)
	if err != nil || !same {
		return !same, err
	}
	same, err = s.check(ctx, append([]string{"diff-files", "--quiet", "--ignore-submodules", "--"}, pathspecs...)...)
	return !same, err
}

// untrackedFiles lists the untracked files a push records as NUL-terminated
// paths relative to the top of the work tree.
func (s *Stack) untrackedFiles(ctx context.Context, mode stow.Untracked, pathspecs []string) ([]byte, error) {
	args := []string{"ls-files", "--others", "-z", "--full-name"}
	if mode != stow.UntrackedAll {
		args = append(args, "--exclude-standard")
	}
	args = append(args, "--")
	if len(pathspecs) == 0 {
		args = append(args, ":/")
	}
	args = append(args, pathspecs...)
	res, err := s.run(ctx, gitCmd{args: args})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("listing untracked files: %s", bytes.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

// record is a stow entry that has been written to the object store but not
// yet stored in the stack.
type record struct {
	commit string // W: tree of the recorded work tree; parents HEAD, I[, U]
	iTree  string // tree of I, the recorded index
	msg    string
	patch  []byte // patch mode only: the diff the work tree loses
}

// record writes the entry's commits. git stash create supplies the index
// commit and the work-tree tree; pathspecs, patch mode and untracked files
// refine them, and the final commit is assembled here.
func (s *Stack) record(ctx context.Context, head string, o stow.PushOptions, untracked []byte) (record, error) {
	branch, base, err := s.describeHead(ctx)
	if err != nil {
		return record{}, err
	}
	ident, err := s.fallbackIdent(ctx)
	if err != nil {
		return record{}, err
	}

	created, err := s.gitOut(ctx, "stash", "create")
	if err != nil {
		return record{}, err
	}
	var iCommit, wTree string
	if created == "" {
		// Nothing tracked changed, so the index and the work tree both match
		// the index as it is; only untracked files are being recorded.
		if wTree, err = s.gitOut(ctx, "write-tree"); err != nil {
			return record{}, err
		}
		if iCommit, err = s.commitTree(ctx, ident, wTree, "index on "+base+"\n", head); err != nil {
			return record{}, err
		}
	} else {
		if iCommit, err = s.gitOut(ctx, "rev-parse", "--verify", created+"^2"); err != nil {
			return record{}, err
		}
		if wTree, err = s.gitOut(ctx, "rev-parse", "--verify", created+"^{tree}"); err != nil {
			return record{}, err
		}
	}
	iTree, err := s.gitOut(ctx, "rev-parse", "--verify", iCommit+"^{tree}")
	if err != nil {
		return record{}, err
	}

	rec := record{iTree: iTree, msg: "WIP on " + base}
	if o.Message != "" {
		rec.msg = "On " + branch + ": " + o.Message
	}

	switch {
	case o.Patch:
		if wTree, rec.patch, err = s.selectHunks(ctx, o.Pathspecs); err != nil {
			return record{}, err
		}
		if len(rec.patch) == 0 {
			if !o.Quiet {
				s.eprintln("No changes selected")
			}
			return record{}, stow.Exit(1)
		}
	case len(o.Pathspecs) > 0:
		if wTree, err = s.restrictTree(ctx, iTree, wTree, o.Pathspecs); err != nil {
			return record{}, err
		}
	}

	parents := []string{head, iCommit}
	if o.Untracked != stow.UntrackedNone {
		uCommit, err := s.untrackedCommit(ctx, ident, untracked, base)
		if err != nil {
			return record{}, err
		}
		parents = append(parents, uCommit)
	}
	if rec.commit, err = s.commitTree(ctx, ident, wTree, rec.msg, parents...); err != nil {
		return record{}, err
	}
	return rec, nil
}

// describeHead returns the branch name git stash puts in its messages and the
// "<branch>: <abbrev> <subject>" description of HEAD.
func (s *Stack) describeHead(ctx context.Context) (branch, base string, err error) {
	branch = "(no branch)"
	ref, err := s.run(ctx, gitCmd{args: []string{"symbolic-ref", "--quiet", "HEAD"}})
	if err != nil {
		return "", "", err
	}
	if name, ok := strings.CutPrefix(strings.TrimSpace(string(ref.Stdout)), "refs/heads/"); ref.ExitCode == 0 && ok {
		branch = name
	}
	oneline, err := s.gitOut(ctx, "log", "-1", "--no-show-signature", "--format=%h %s", "HEAD", "--")
	if err != nil {
		return "", "", err
	}
	return branch, branch + ": " + oneline, nil
}

// fallbackIdent mirrors git stash's prepare_fallback_ident: commits are made
// even when user.name or user.email is unset, under git stash's own fallback
// identity so that git-stow's commits stay byte-identical to git stash's.
func (s *Stack) fallbackIdent(ctx context.Context) ([]string, error) {
	var env []string
	for _, f := range []struct{ key, value string }{{"name", "git stash"}, {"email", "git@stash"}} {
		set, err := s.check(ctx, "config", "--get", "user."+f.key)
		if err != nil {
			return nil, err
		}
		if set {
			continue
		}
		for _, role := range []string{"AUTHOR", "COMMITTER"} {
			v := "GIT_" + role + "_" + strings.ToUpper(f.key)
			if _, ok := os.LookupEnv(v); !ok {
				env = append(env, v+"="+f.value)
			}
		}
	}
	return env, nil
}

// commitTree writes a commit whose message is exactly msg. git stash ends the
// index and untracked messages with a newline but not the entry's own.
func (s *Stack) commitTree(ctx context.Context, ident []string, tree, msg string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return s.exec(ctx, gitCmd{args: args, env: ident, stdin: strings.NewReader(msg)})
}

// withTempIndex runs fn with a private index file that is removed afterwards.
func (s *Stack) withTempIndex(ctx context.Context, fn func(env []string) error) error {
	path, err := s.tempIndexPath(ctx)
	if err != nil {
		return err
	}
	os.Remove(path)
	defer os.Remove(path)
	return fn([]string{"GIT_INDEX_FILE=" + path})
}

// untrackedCommit records the untracked files as a root commit, as git stash's
// save_untracked_files does.
func (s *Stack) untrackedCommit(ctx context.Context, ident []string, files []byte, base string) (string, error) {
	var tree string
	err := s.withTempIndex(ctx, func(env []string) error {
		if _, err := s.exec(ctx, gitCmd{
			args:  []string{"update-index", "-z", "--add", "--remove", "--stdin"},
			atTop: true, env: env, stdin: bytes.NewReader(files),
		}); err != nil {
			return err
		}
		var err error
		tree, err = s.exec(ctx, gitCmd{args: []string{"write-tree"}, env: env})
		return err
	})
	if err != nil {
		return "", err
	}
	return s.commitTree(ctx, ident, tree, "untracked files on "+base+"\n")
}

// restrictTree narrows the work-tree state git stash create recorded for every
// path to the pathspecs: paths outside them keep their recorded index state,
// which is what git stash push records for a pathspec-limited stash.
func (s *Stack) restrictTree(ctx context.Context, iTree, wTree string, pathspecs []string) (string, error) {
	args := append([]string{"diff-tree", "-r", "-z", "--no-renames", iTree, wTree, "--"}, pathspecs...)
	raw, err := s.exec(ctx, gitCmd{args: args})
	if err != nil {
		return "", err
	}
	info, err := indexInfo(raw, len(iTree))
	if err != nil {
		return "", err
	}
	if len(info) == 0 {
		return iTree, nil
	}

	var tree string
	err = s.withTempIndex(ctx, func(env []string) error {
		if _, err := s.exec(ctx, gitCmd{args: []string{"read-tree", iTree}, env: env}); err != nil {
			return err
		}
		if _, err := s.exec(ctx, gitCmd{
			args:  []string{"update-index", "-z", "--index-info"},
			atTop: true, env: env, stdin: bytes.NewReader(info),
		}); err != nil {
			return err
		}
		var err error
		tree, err = s.exec(ctx, gitCmd{args: []string{"write-tree"}, env: env})
		return err
	})
	return tree, err
}

// indexInfo converts `git diff-tree -r -z` raw output into
// `git update-index -z --index-info` input that sets every listed path to its
// post-image, removing deleted paths.
func indexInfo(raw string, oidLen int) ([]byte, error) {
	fields := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	if len(fields) == 1 && fields[0] == "" {
		return nil, nil
	}
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("unexpected diff-tree output %q", raw)
	}
	zero := strings.Repeat("0", oidLen)
	var buf bytes.Buffer
	for i := 0; i < len(fields); i += 2 {
		// ":<old mode> <new mode> <old oid> <new oid> <status>"
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) != 5 {
			return nil, fmt.Errorf("unexpected diff-tree record %q", fields[i])
		}
		path := fields[i+1]
		if meta[4] == "D" {
			fmt.Fprintf(&buf, "0 %s\t%s\x00", zero, path)
		} else {
			fmt.Fprintf(&buf, "%s %s\t%s\x00", meta[1], meta[3], path)
		}
	}
	return buf.Bytes(), nil
}

// selectHunks lets the user pick hunks with `git add -p` against a private
// index seeded from HEAD, which is how git stash -p collects them. It returns
// the resulting tree and its diff from HEAD.
func (s *Stack) selectHunks(ctx context.Context, pathspecs []string) (string, []byte, error) {
	var tree string
	err := s.withTempIndex(ctx, func(env []string) error {
		if _, err := s.exec(ctx, gitCmd{args: []string{"read-tree", "HEAD"}, env: env}); err != nil {
			return err
		}
		res, err := s.run(ctx, gitCmd{
			args: append([]string{"add", "--patch", "--"}, pathspecs...),
			env:  env, stdin: s.io.In, passthrough: true,
		})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			s.eprintln("Cannot save the current worktree state")
			return stow.Exit(1)
		}
		tree, err = s.exec(ctx, gitCmd{args: []string{"write-tree"}, env: env})
		return err
	})
	if err != nil {
		return "", nil, err
	}
	res, err := s.run(ctx, gitCmd{args: []string{"diff-tree", "-p", "-U1", "HEAD", tree, "--"}})
	if err != nil {
		return "", nil, err
	}
	if res.ExitCode != 0 {
		return "", nil, fmt.Errorf("git diff-tree: %s", bytes.TrimSpace(res.Stderr))
	}
	return tree, res.Stdout, nil
}

// reset removes what a non-patch push recorded from the work tree and index.
func (s *Stack) reset(ctx context.Context, rec record, keep stow.KeepIndex, o stow.PushOptions) error {
	if o.Untracked != stow.UntrackedNone && len(o.Pathspecs) == 0 {
		args := []string{"clean", "--force", "--quiet", "-d", ":/"}
		if o.Untracked == stow.UntrackedAll {
			args = append(args, "-x")
		}
		if err := s.step(ctx, gitCmd{args: args}); err != nil {
			return err
		}
	}

	if len(o.Pathspecs) > 0 {
		add := []string{"add"}
		if o.Untracked == stow.UntrackedNone {
			add = append(add, "-u")
		}
		if o.Untracked == stow.UntrackedAll {
			add = append(add, "--force")
		}
		if err := s.step(ctx, gitCmd{args: append(append(add, "--"), o.Pathspecs...)}); err != nil {
			return err
		}
		diff, err := s.run(ctx, gitCmd{args: append([]string{"diff-index", "-p", "--cached", "--binary", "HEAD", "--"}, o.Pathspecs...)})
		if err != nil {
			return err
		}
		if diff.ExitCode != 0 {
			s.io.Err.Write(diff.Stderr)
			return stow.Exit(1)
		}
		if err := s.step(ctx, gitCmd{args: []string{"apply", "--index", "-R"}, atTop: true, stdin: bytes.NewReader(diff.Stdout)}); err != nil {
			return err
		}
	} else if err := s.step(ctx, gitCmd{args: []string{"reset", "--hard", "-q", "--no-recurse-submodules"}}); err != nil {
		return err
	}

	if keep == stow.KeepIndexOn {
		args := []string{"checkout", "--no-overlay", rec.iTree, "--"}
		if len(o.Pathspecs) == 0 {
			args = append(args, ":/")
		}
		if err := s.step(ctx, gitCmd{args: append(args, o.Pathspecs...)}); err != nil {
			return err
		}
	}
	return nil
}

// resetPatch removes the selected hunks from the work tree and, with
// --no-keep-index, unstages the pathspecs.
func (s *Stack) resetPatch(ctx context.Context, rec record, keep stow.KeepIndex, pathspecs []string) error {
	if err := s.step(ctx, gitCmd{args: []string{"apply", "-R"}, atTop: true, stdin: bytes.NewReader(rec.patch)}); err != nil {
		s.eprintln("Cannot remove worktree changes")
		return stow.Exit(1)
	}
	if keep == stow.KeepIndexOff {
		args := append([]string{"reset", "-q", "--refresh", "--"}, pathspecs...)
		if err := s.step(ctx, gitCmd{args: args}); err != nil {
			return err
		}
	}
	return nil
}
