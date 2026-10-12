// Package git defines the contract git-stow uses to run the git binary.
package git

import (
	"context"
	"io"
)

// Cmd describes one git invocation.
type Cmd struct {
	// Dir is the absolute working directory. It is required: git-stow never
	// lets a git process inherit an implicit directory.
	Dir  string
	Args []string
	// Env holds KEY=VALUE entries appended to the inherited environment.
	Env   []string
	Stdin io.Reader
	// Stdout and Stderr, when nil, are captured into the Result.
	Stdout io.Writer
	Stderr io.Writer
}

// Result is the outcome of a git process that ran to completion.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs git commands.
type Runner interface {
	// Run executes cmd and waits for it to exit. A non-zero exit status is
	// reported in Result.ExitCode; the error is non-nil only when git could not
	// be started or ctx was cancelled. Run panics when cmd.Dir is not absolute.
	// Safe for concurrent use.
	Run(ctx context.Context, cmd Cmd) (Result, error)
}
