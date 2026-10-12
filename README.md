# git-stow

`git stash` with a separate stack for every worktree.

`git stash` keeps a single `refs/stash`, and every worktree of a clone shares it:
`git stash clear` in one worktree wipes the stash of all of them, and
`stash@{0}` means "whatever was stashed last, in any worktree". git-stow is
`git stash` with storage per checkout: each worktree, the main checkout
included, has its own stack, and nothing done in one reaches another.

```console
$ git stow                 # stash this worktree's changes
$ git stow list
stow@{0}: WIP on feature: 1a2b3c4 Add the parser
$ git stow pop
```

## Install

```sh
go install github.com/brynbellomy/git-stow/cmd/git-stow@latest
```

This puts a `git-stow` binary in `$(go env GOPATH)/bin`. With that directory on
`PATH`, git runs it as `git stow`. From a checkout, `make install` does the same.

git-stow shells out to the `git` on `PATH`. It is tested against git 2.45.

## Commands

Entries are addressed `stow@{n}`, newest first; a bare number `n` means
`stow@{n}`, and an omitted entry means `stow@{0}`. `apply`, `show` and `branch`
also accept any stash-like commit, such as an object id.

| Command | Does |
|---|---|
| `git stow` | Same as `git stow push`. Options are allowed (`git stow -u`); a pathspec needs `--` (`git stow -u -- src/`). |
| `git stow push [-k \| --[no-]keep-index] [-u \| --include-untracked] [-a \| --all] [-p \| --patch] [-q] [-m <message>] [--] [<pathspec>...]` | Record the local changes as `stow@{0}` and reset them, exactly as `git stash push`. |
| `git stow list [<log-options>]` | List the entries of this worktree. |
| `git stow show [-u \| --include-untracked \| --only-untracked] [<diff-options>] [<entry>]` | Show an entry's changes (`git stash show`). |
| `git stow apply [--index] [-q] [<entry>]` | Apply an entry and keep it. |
| `git stow pop [--index] [-q] [<entry>]` | Apply an entry and drop it; on a conflict the entry is kept. |
| `git stow drop [-q] [<entry>]` | Drop an entry. |
| `git stow clear` | Drop every entry of this worktree. Other worktrees keep theirs. |
| `git stow branch <name> [<entry>]` | Create and check out `<name>` at the entry's base commit, apply the entry with `--index`, and drop it. |

Options, messages and exit statuses are `git stash`'s. Short options bundle
(`-ku`), long options take unique abbreviations (`--keep`), and
`--no-keep-index`, `--no-include-untracked` and `--no-all` undo earlier flags.

## How it differs from `git stash`

Every command of the table above runs against a differential test suite that
performs each operation with `git stash` in one repository and `git stow` in an
identical one, and checks that the work tree, index, HEAD, exit status, output
and entry commits match. The commits are byte-identical. These are the
differences that remain:

- **One stack per worktree.** `list`, `pop`, `drop` and `clear` see only the
  current worktree's entries. `git stash` and `git stow` never touch each
  other's entries.
- **Names.** Entries are `stow@{n}` and messages say "stow" where `git stash`
  says "stash": `No stow entries found.`, `'HEAD' is not a stow reference`,
  `The stow entry is kept in case you need it again.`,
  `Dropped stow@{0} (<oid>)`. `git stash drop` prints the entry as
  `refs/stash@{n}` or `stash@{n}`, depending on how it was given;
  `git stow drop` always prints `stow@{n}`.
- **Reflog expiry.** git never expires `refs/stash` entries unless configured
  to, but it expires entries of every other reflog after
  `gc.reflogExpireUnreachable` (30 days by default), and `git gc` runs that
  expiry. To give stow entries the same lifetime, the first `git stow push` in
  a repository sets `gc.*refs/worktree/stow.reflogExpire` and
  `gc.*refs/worktree/stow.reflogExpireUnreachable` to `never` in the
  repository's config. A value already set in any config file is left alone.
- **Removing a worktree removes its stack.** `git worktree remove` and
  `git worktree prune` delete the worktree's private git directory, and its
  stow entries with it. Pop or move what you want to keep first, for example
  with `git stow branch`.
- **Unmatched pathspecs** are reported as typed. `git stash` prints its
  internal form, `pathspec ':(prefix:0)nope.txt' did not match`.
- **`--patch`** collects hunks with `git add -p` against a private index, so
  its prompts ask "Stage this hunk" where `git stash -p` asks "Stash this
  hunk". The answers, and the entry they produce, are the same.
- **`list`** is not paged.
- **Pushes are compare-and-swap.** A push stores its entry only if the stack
  has not moved since it started, and `pop` and `drop` refuse to drop an entry
  that changed while they ran. Two pushes racing in one worktree never lose an
  entry that was reported saved.
- **Not supported:** `-S`/`--staged`, `--pathspec-from-file`,
  `--pathspec-file-nul`, and the `save`, `create` and `store` subcommands. The
  options are refused with `fatal: git stow does not support <option>`.

As in `git stash`, a pathspec after an omitted `push` needs `--`:
`git stow -p a.txt` fails just as `git stash -p a.txt` does in git 2.45,
although git's documentation describes `-p` as an exception.

## Storage model

An entry is the commit `git stash` would make: a work-tree commit W whose
parents are HEAD and an index commit I, plus an untracked-files commit U as
third parent when `-u` or `-a` recorded untracked files. W is stored in the
per-worktree ref `refs/worktree/stow`, and the ref's reflog is the stack:
`stow@{n}` is `refs/worktree/stow@{n}`, just as `stash@{n}` is
`refs/stash@{n}`.

git keeps every `refs/worktree/*` ref, and its reflog, private to one
worktree. The main checkout stores them in `.git/refs/worktree/` and
`.git/logs/refs/worktree/`, and a linked worktree stores them in
`.git/worktrees/<name>/`. Objects are shared, and `git gc` treats every
worktree's stow reflog as reachable.

Because entries are ordinary stash commits, git's own tools read them:

```sh
git stash show -p worktree/stow@{1}                 # git's name for stow@{1}
git stash apply "$(git rev-parse worktree/stow@{0})"
git log -g main-worktree/refs/worktree/stow         # the main checkout's stack, from any worktree
git log -g worktrees/<name>/refs/worktree/stow      # a linked worktree's stack
```

What `git stash` exposes, git-stow delegates to it: `git stash create` records
the index and work tree, and `git stash apply`, `show` and `branch` act on
entries by object id. git-stow implements only what `git stash` does not
expose (the untracked-files commit, narrowing an entry to a pathspec, and the
reset after a push), step for step after git's `builtin/stash.c`, with git
plumbing.

## Development

```sh
make check   # gofmt, go vet, go test
make build   # bin/git-stow
```

The tests run the real `git` and the built `git-stow` against throwaway
repositories. Every repository lives under one temporary directory, and every
test package starts from a fixture repository whose canaries (a `git stash`
entry, an uncommitted change and an untracked file) must be intact when the
run ends. See `AGENTS.md` for the rules.

## License

MIT. See [LICENSE](LICENSE).
