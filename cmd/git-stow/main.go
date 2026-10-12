// Command git-stow is git stash with a separate stack per worktree. Installed
// on PATH it runs as `git stow`.
package main

import (
	"context"
	"os"

	"github.com/brynbellomy/git-stow/internal/cli"
	"github.com/brynbellomy/git-stow/internal/git/execgit"
	"github.com/brynbellomy/git-stow/internal/stow"
	"github.com/brynbellomy/git-stow/internal/stow/gitstack"
)

func main() {
	streams := gitstack.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	open := func(ctx context.Context) (stow.Stack, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		return gitstack.New(ctx, execgit.New(), cwd, streams)
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], open, os.Stdout, os.Stderr))
}
