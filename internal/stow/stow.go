// Package stow defines the per-worktree stash stack that git-stow exposes.
//
// Entries use git stash's commit format and are addressed stow@{n}, newest
// first. Every operation affects only the worktree it runs in.
package stow

import (
	"context"
	"fmt"
)

// Untracked selects which untracked files a push records.
type Untracked int

const (
	// UntrackedNone records tracked changes only.
	UntrackedNone Untracked = iota
	// UntrackedInclude also records untracked, non-ignored files (-u).
	UntrackedInclude
	// UntrackedAll also records untracked and ignored files (-a).
	UntrackedAll
)

// KeepIndex is the tri-state --keep-index flag; unset means "the mode's default".
type KeepIndex int

const (
	KeepIndexUnset KeepIndex = iota
	KeepIndexOff
	KeepIndexOn
)

// PushOptions are the options of `git stow push`.
type PushOptions struct {
	Message   string
	KeepIndex KeepIndex
	Patch     bool
	Untracked Untracked
	Quiet     bool
	Pathspecs []string
}

// ApplyOptions are the options of `git stow apply` and `git stow pop`.
type ApplyOptions struct {
	// Entry is stow@{n}, n, or (apply only) any commit-ish; empty means stow@{0}.
	Entry string
	Index bool
	Quiet bool
}

// DropOptions are the options of `git stow drop`.
type DropOptions struct {
	Entry string
	Quiet bool
}

// Stack is the stow stack of the current worktree. Every method writes git's
// own messages to the configured output streams and returns *ExitStatus when
// the command fails in a way the user has already been told about.
type Stack interface {
	// Push records the local changes as a new stow@{0} and resets them.
	Push(ctx context.Context, opts PushOptions) error
	// List prints the entries; logArgs are passed to git log.
	List(ctx context.Context, logArgs []string) error
	// Show prints the diff of an entry; diffArgs are passed to git stash show.
	Show(ctx context.Context, diffArgs []string, entry string) error
	// Apply applies an entry and keeps it.
	Apply(ctx context.Context, opts ApplyOptions) error
	// Pop applies an entry and drops it when the apply succeeds.
	Pop(ctx context.Context, opts ApplyOptions) error
	// Drop removes an entry.
	Drop(ctx context.Context, opts DropOptions) error
	// Clear removes every entry of this worktree.
	Clear(ctx context.Context) error
	// Branch creates and checks out a branch at the entry's base commit,
	// applies the entry there, and drops it when the apply succeeds.
	Branch(ctx context.Context, name, entry string) error
}

// ExitStatus reports a failure whose message was already printed; the process
// exits with Code.
type ExitStatus struct {
	Code int
}

func (e *ExitStatus) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// Exit returns an *ExitStatus with the given code.
func Exit(code int) error {
	return &ExitStatus{Code: code}
}
