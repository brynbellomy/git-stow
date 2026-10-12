// Package cli parses git-stow's command line and dispatches it to a
// stow.Stack. The surface and messages mirror git stash's.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/brynbellomy/git-stow/internal/stow"
)

// Command is a parsed git-stow command line.
type Command struct {
	// Name is the subcommand: push, list, show, apply, pop, drop, clear or branch.
	Name  string
	Push  stow.PushOptions
	Apply stow.ApplyOptions // apply and pop
	Drop  stow.DropOptions
	// Args holds the arguments list passes to git log and show passes to git
	// stash show.
	Args []string
	// Entry is the entry argument of show and branch.
	Entry string
	// Branch is the branch name of branch.
	Branch string
}

const usage = `usage: git stow list [<log-options>]
   or: git stow show [-u | --include-untracked | --only-untracked] [<diff-options>] [<stow>]
   or: git stow drop [-q | --quiet] [<stow>]
   or: git stow pop [--index] [-q | --quiet] [<stow>]
   or: git stow apply [--index] [-q | --quiet] [<stow>]
   or: git stow branch <branchname> [<stow>]
   or: git stow clear
   or: git stow [push [-p | --patch] [-k | --[no-]keep-index] [-q | --quiet]
          [-u | --include-untracked] [-a | --all] [(-m | --message) <message>]
          [--] [<pathspec>...]]
`

// Parse parses the arguments that follow "git stow".
func Parse(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{Name: "push"}, nil
	}
	name, rest := args[0], args[1:]
	switch name {
	case "push":
		return parsePush(rest, false)
	case "list":
		return Command{Name: name, Args: rest}, nil
	case "show":
		return parseShow(rest)
	case "apply", "pop":
		return parseApply(name, rest)
	case "drop":
		return parseDrop(rest)
	case "clear":
		return parseClear(rest)
	case "branch":
		return parseBranch(rest)
	}
	// Like git stash, anything else is a push with "push" omitted.
	return parsePush(args, true)
}

func parsePush(args []string, assumed bool) (Command, error) {
	cmd := Command{Name: "push"}
	o := &cmd.Push
	var help bool
	opts := []option{
		{short: 'k', long: "keep-index", negatable: true, set: func(_ string, neg bool) error {
			o.KeepIndex = stow.KeepIndexOn
			if neg {
				o.KeepIndex = stow.KeepIndexOff
			}
			return nil
		}},
		{short: 'p', long: "patch", negatable: true, set: flag(&o.Patch)},
		{short: 'q', long: "quiet", negatable: true, set: flag(&o.Quiet)},
		{short: 'u', long: "include-untracked", negatable: true, set: func(_ string, neg bool) error {
			o.Untracked = stow.UntrackedInclude
			if neg {
				o.Untracked = stow.UntrackedNone
			}
			return nil
		}},
		{short: 'a', long: "all", negatable: true, set: func(_ string, neg bool) error {
			o.Untracked = stow.UntrackedAll
			if neg {
				o.Untracked = stow.UntrackedNone
			}
			return nil
		}},
		{short: 'm', long: "message", takesArg: true, set: func(v string, _ bool) error {
			o.Message = v
			return nil
		}},
		{short: 'S', long: "staged", set: unsupported("--staged")},
		{long: "pathspec-from-file", takesArg: true, set: unsupported("--pathspec-from-file")},
		{long: "pathspec-file-nul", set: unsupported("--pathspec-file-nul")},
		{short: 'h', set: flag(&help)},
	}
	operands, err := parseOptions(args, opts, true)
	if err == nil && help {
		err = &helpRequest{}
	}
	if err != nil {
		return Command{}, err
	}
	if len(operands) > 0 {
		if operands[0] == "--" {
			operands = operands[1:]
		} else if assumed {
			// git documents `git stash -p <pathspec>` as an exception, but its
			// parser never applies it; git stow refuses it the same way.
			return Command{}, fatalf("subcommand wasn't specified; 'push' can't be assumed due to unexpected token '%s'", operands[0])
		}
	}
	o.Pathspecs = operands
	return cmd, nil
}

func parseShow(args []string) (Command, error) {
	cmd := Command{Name: "show"}
	var entries []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			cmd.Args = append(cmd.Args, a)
		} else {
			entries = append(entries, a)
		}
	}
	entry, err := oneEntry(entries)
	cmd.Entry = entry
	return cmd, err
}

func parseApply(name string, args []string) (Command, error) {
	cmd := Command{Name: name}
	var help bool
	opts := []option{
		{long: "index", negatable: true, set: flag(&cmd.Apply.Index)},
		{short: 'q', long: "quiet", negatable: true, set: flag(&cmd.Apply.Quiet)},
		{short: 'h', set: flag(&help)},
	}
	operands, err := parseOptions(args, opts, false)
	if err == nil && help {
		err = &helpRequest{}
	}
	if err != nil {
		return Command{}, err
	}
	cmd.Apply.Entry, err = oneEntry(operands)
	return cmd, err
}

func parseDrop(args []string) (Command, error) {
	cmd := Command{Name: "drop"}
	var help bool
	opts := []option{
		{short: 'q', long: "quiet", negatable: true, set: flag(&cmd.Drop.Quiet)},
		{short: 'h', set: flag(&help)},
	}
	operands, err := parseOptions(args, opts, false)
	if err == nil && help {
		err = &helpRequest{}
	}
	if err != nil {
		return Command{}, err
	}
	cmd.Drop.Entry, err = oneEntry(operands)
	return cmd, err
}

func parseClear(args []string) (Command, error) {
	var help bool
	operands, err := parseOptions(args, []option{{short: 'h', set: flag(&help)}}, false)
	if err == nil && help {
		err = &helpRequest{}
	}
	if err != nil {
		return Command{}, err
	}
	if len(operands) > 0 {
		return Command{}, failf("error: git stow clear with arguments is unimplemented")
	}
	return Command{Name: "clear"}, nil
}

func parseBranch(args []string) (Command, error) {
	var help bool
	operands, err := parseOptions(args, []option{{short: 'h', set: flag(&help)}}, false)
	if err == nil && help {
		err = &helpRequest{}
	}
	if err != nil {
		return Command{}, err
	}
	if len(operands) == 0 {
		return Command{}, failf("No branch name specified")
	}
	entry, err := oneEntry(operands[1:])
	return Command{Name: "branch", Branch: operands[0], Entry: entry}, err
}

// oneEntry enforces git stash's "at most one revision" rule.
func oneEntry(operands []string) (string, error) {
	switch len(operands) {
	case 0:
		return "", nil
	case 1:
		return operands[0], nil
	}
	quoted := make([]string, len(operands))
	for i, o := range operands {
		quoted[i] = "'" + o + "'"
	}
	return "", failf("Too many revisions specified: %s", strings.Join(quoted, " "))
}

// unsupported is the setter of git stash options git-stow refuses.
func unsupported(name string) func(string, bool) error {
	return func(string, bool) error {
		return fatalf("git stow does not support %s", name)
	}
}

// failure is an error whose message is printed verbatim before exiting with code.
type failure struct {
	msg  string
	code int
}

func (f *failure) Error() string {
	return f.msg
}

func failf(format string, a ...any) error {
	return &failure{msg: fmt.Sprintf(format, a...), code: 1}
}

func fatalf(format string, a ...any) error {
	return &failure{msg: "fatal: " + fmt.Sprintf(format, a...), code: 128}
}

// helpRequest is -h: the usage goes to stdout and the exit status is 129.
type helpRequest struct{}

func (*helpRequest) Error() string {
	return "help requested"
}

// Run parses args, opens the stack with open and runs the command. It returns
// the process exit status.
func Run(ctx context.Context, args []string, open func(context.Context) (stow.Stack, error), stdout, stderr io.Writer) int {
	cmd, err := Parse(args)
	if err == nil {
		var stack stow.Stack
		if stack, err = open(ctx); err == nil {
			err = dispatch(ctx, stack, cmd)
		}
	}
	return report(err, stdout, stderr)
}

func dispatch(ctx context.Context, s stow.Stack, cmd Command) error {
	switch cmd.Name {
	case "push":
		return s.Push(ctx, cmd.Push)
	case "list":
		return s.List(ctx, cmd.Args)
	case "show":
		return s.Show(ctx, cmd.Args, cmd.Entry)
	case "apply":
		return s.Apply(ctx, cmd.Apply)
	case "pop":
		return s.Pop(ctx, cmd.Apply)
	case "drop":
		return s.Drop(ctx, cmd.Drop)
	case "clear":
		return s.Clear(ctx)
	case "branch":
		return s.Branch(ctx, cmd.Branch, cmd.Entry)
	}
	panic(fmt.Sprintf("cli: Parse produced unknown command %q", cmd.Name))
}

// report prints err the way git would and returns the exit status.
func report(err error, stdout, stderr io.Writer) int {
	var (
		status *stow.ExitStatus
		usageE *usageError
		help   *helpRequest
		fail   *failure
	)
	switch {
	case err == nil:
		return 0
	case errors.As(err, &status):
		return status.Code
	case errors.As(err, &help):
		fmt.Fprint(stdout, usage)
		return 129
	case errors.As(err, &usageE):
		fmt.Fprintf(stderr, "error: %s\n%s", usageE.msg, usage)
		return 129
	case errors.As(err, &fail):
		fmt.Fprintln(stderr, fail.msg)
		return fail.code
	default:
		fmt.Fprintf(stderr, "fatal: %v\n", err)
		return 128
	}
}
