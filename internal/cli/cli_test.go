package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brynbellomy/git-stow/internal/gittest"
	"github.com/brynbellomy/git-stow/internal/stow"
)

func TestMain(m *testing.M) {
	gittest.Main(m)
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Command
	}{
		{"bare is push", nil, Command{Name: "push"}},
		{"push", []string{"push"}, Command{Name: "push"}},
		{"assumed push with options", []string{"-u", "-m", "msg"}, Command{Name: "push", Push: stow.PushOptions{Untracked: stow.UntrackedInclude, Message: "msg"}}},
		{"bundled shorts", []string{"push", "-ukq"}, Command{Name: "push", Push: stow.PushOptions{Untracked: stow.UntrackedInclude, KeepIndex: stow.KeepIndexOn, Quiet: true}}},
		{"attached short value", []string{"push", "-mhello"}, Command{Name: "push", Push: stow.PushOptions{Message: "hello"}}},
		{"long value with equals", []string{"push", "--message=a b"}, Command{Name: "push", Push: stow.PushOptions{Message: "a b"}}},
		{"abbreviated long", []string{"push", "--keep", "--include"}, Command{Name: "push", Push: stow.PushOptions{KeepIndex: stow.KeepIndexOn, Untracked: stow.UntrackedInclude}}},
		{"negated", []string{"push", "-a", "--no-all", "--no-keep-index"}, Command{Name: "push", Push: stow.PushOptions{KeepIndex: stow.KeepIndexOff}}},
		{"all", []string{"push", "--all"}, Command{Name: "push", Push: stow.PushOptions{Untracked: stow.UntrackedAll}}},
		{"patch", []string{"push", "-p"}, Command{Name: "push", Push: stow.PushOptions{Patch: true}}},
		{"pathspecs interleaved", []string{"push", "a", "-u", "b"}, Command{Name: "push", Push: stow.PushOptions{Untracked: stow.UntrackedInclude, Pathspecs: []string{"a", "b"}}}},
		{"pathspecs after dashdash", []string{"push", "--", "-u", "b"}, Command{Name: "push", Push: stow.PushOptions{Pathspecs: []string{"-u", "b"}}}},
		{"assumed push with dashdash", []string{"-u", "--", "a"}, Command{Name: "push", Push: stow.PushOptions{Untracked: stow.UntrackedInclude, Pathspecs: []string{"a"}}}},
		{"list passes log options", []string{"list", "--stat", "-2"}, Command{Name: "list", Args: []string{"--stat", "-2"}}},
		{"show", []string{"show", "-p", "--stat", "stow@{1}"}, Command{Name: "show", Args: []string{"-p", "--stat"}, Entry: "stow@{1}"}},
		{"apply", []string{"apply", "--index", "-q", "2"}, Command{Name: "apply", Apply: stow.ApplyOptions{Entry: "2", Index: true, Quiet: true}}},
		{"pop", []string{"pop"}, Command{Name: "pop"}},
		{"drop", []string{"drop", "--quiet", "stow@{3}"}, Command{Name: "drop", Drop: stow.DropOptions{Entry: "stow@{3}", Quiet: true}}},
		{"clear", []string{"clear"}, Command{Name: "clear"}},
		{"branch", []string{"branch", "topic", "1"}, Command{Name: "branch", Branch: "topic", Entry: "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stderr string
		stdout string
	}{
		{"unknown subcommand", []string{"frobnicate"}, 128, "fatal: subcommand wasn't specified; 'push' can't be assumed due to unexpected token 'frobnicate'\n", ""},
		{"assumed push with pathspec", []string{"-u", "a.txt"}, 128, "fatal: subcommand wasn't specified; 'push' can't be assumed due to unexpected token 'a.txt'\n", ""},
		{"unknown long option", []string{"push", "--bogus"}, 129, "error: unknown option `bogus'\n" + usage, ""},
		{"unknown switch", []string{"push", "-z"}, 129, "error: unknown switch `z'\n" + usage, ""},
		{"ambiguous abbreviation", []string{"push", "--no-"}, 129, "error: ambiguous option: no-\n" + usage, ""},
		{"missing value", []string{"push", "-m"}, 129, "error: switch `m' requires a value\n" + usage, ""},
		{"missing long value", []string{"push", "--message"}, 129, "error: option `message' requires a value\n" + usage, ""},
		{"help", []string{"push", "-h"}, 129, "", usage},
		{"staged is refused", []string{"push", "--staged"}, 128, "fatal: git stow does not support --staged\n", ""},
		{"pathspec-from-file is refused", []string{"--pathspec-from-file=x"}, 128, "fatal: git stow does not support --pathspec-from-file\n", ""},
		{"too many entries", []string{"pop", "1", "2"}, 1, "Too many revisions specified: '1' '2'\n", ""},
		{"too many show entries", []string{"show", "a", "b"}, 1, "Too many revisions specified: 'a' 'b'\n", ""},
		{"clear with arguments", []string{"clear", "x"}, 1, "error: git stow clear with arguments is unimplemented\n", ""},
		{"branch without name", []string{"branch"}, 1, "No branch name specified\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			open := func(ctx context.Context) (stow.Stack, error) {
				t.Fatal("a command line that does not parse must not open the stack")
				return nil, nil
			}
			code := Run(context.Background(), tt.args, open, &stdout, &stderr)
			require.Equal(t, tt.code, code)
			require.Equal(t, tt.stderr, stderr.String())
			require.Equal(t, tt.stdout, stdout.String())
		})
	}
}
