// Package e2e runs the git-stow binary against real repositories: the
// differential conformance suite against git stash, worktree isolation,
// concurrency and storage durability.
package e2e

import (
	"testing"

	"github.com/brynbellomy/git-stow/internal/gittest"
)

func TestMain(m *testing.M) {
	gittest.Main(m)
}
