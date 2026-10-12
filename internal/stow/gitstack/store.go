package gitstack

import (
	"context"
	"fmt"
	"strings"

	"github.com/brynbellomy/git-stow/internal/stow"
)

// gcProtectionKeys keep reflog expiry away from stow entries. git exempts
// refs/stash from expiry by name; any other reflog, refs/worktree/stow
// included, loses unreachable entries after gc.reflogExpireUnreachable (30
// days). The pattern is matched against the names `git reflog expire --all`
// uses from every worktree: refs/worktree/stow, main-worktree/refs/worktree/stow
// and worktrees/<name>/refs/worktree/stow.
var gcProtectionKeys = []string{
	"gc.*refs/worktree/stow.reflogExpire",
	"gc.*refs/worktree/stow.reflogExpireUnreachable",
}

// ensureGCProtection sets the gcProtectionKeys to "never" in the repository's
// config unless some config scope already sets them.
func (s *Stack) ensureGCProtection(ctx context.Context) error {
	for _, key := range gcProtectionKeys {
		set, err := s.check(ctx, "config", "--get", key)
		if err != nil {
			return err
		}
		if set {
			continue
		}
		res, err := s.run(ctx, gitCmd{args: []string{"config", "--local", key, "never"}})
		if err != nil {
			return err
		}
		if res.ExitCode == 0 {
			continue
		}
		// Another worktree may hold the config lock while writing the same key.
		if set, err := s.check(ctx, "config", "--get", key); err != nil || !set {
			return fmt.Errorf("setting %s: %s", key, strings.TrimSpace(string(res.Stderr)))
		}
	}
	return nil
}

// store makes commit the new stow@{0}. The update is a compare-and-swap
// against the tip read beforehand, so a concurrent push in the same worktree
// fails this one instead of silently interleaving.
func (s *Stack) store(ctx context.Context, commit, msg string) error {
	old, err := s.resolve(ctx, Ref)
	if err != nil {
		return err
	}
	res, err := s.run(ctx, gitCmd{args: []string{"update-ref", "--create-reflog", "-m", msg, Ref, commit, old}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		s.io.Err.Write(res.Stderr)
		s.eprintln("Cannot save the current status")
		return stow.Exit(1)
	}
	return nil
}

// drop deletes e from the reflog, then removes the ref once the reflog is
// empty, as git stash's do_drop_stash does.
func (s *Stack) drop(ctx context.Context, e entry, quiet bool) error {
	// pop applies before it drops; refuse if the stack moved meanwhile.
	cur, err := s.resolve(ctx, e.rev)
	if err != nil {
		return err
	}
	if cur != e.oid {
		return s.errorf("%s changed while this command ran; not dropping it", e.name)
	}

	res, err := s.run(ctx, gitCmd{args: []string{"reflog", "delete", "--updateref", "--rewrite", e.rev}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		s.io.Err.Write(res.Stderr)
		return s.errorf("%s: Could not drop stow entry", e.name)
	}
	if !quiet {
		s.println(fmt.Sprintf("Dropped %s (%s)", e.name, e.oid))
	}

	remaining, err := s.gitOut(ctx, "reflog", "show", "--format=%H", Ref, "--")
	if err != nil {
		return err
	}
	if remaining == "" {
		return s.clear(ctx)
	}
	return nil
}

// clear deletes the ref and its reflog.
func (s *Stack) clear(ctx context.Context) error {
	tip, err := s.resolve(ctx, Ref)
	if err != nil || tip == "" {
		return err
	}
	_, err = s.gitOut(ctx, "update-ref", "-d", Ref, tip)
	return err
}
