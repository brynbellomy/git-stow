# git-stow

this tool is git-stash, except not shared among worktrees/checkouts. `README.md`
describes the behaviour and the storage model; this file holds the rules for
working on it.

## Layout

- `cmd/git-stow`: wiring only.
- `internal/cli`: command-line parsing (a parse-options clone) and dispatch.
- `internal/stow`: the `Stack` contract and its option types.
- `internal/stow/gitstack`: the implementation. It follows git's
  `builtin/stash.c` step for step; when you change behaviour, read the matching
  function there first.
- `internal/git`: the `Runner` contract. `internal/git/execgit` is the only code
  that starts a process.
- `internal/gittest`: the test safety harness.
- `e2e`: tests that run the built binary against real repositories.

## Rules

- Shell out to git. No go-git, no reimplementing what `git stash` exposes:
  `git stash create`, `apply`, `show` and `branch` do that work, and git-stow
  builds only what git does not expose.
- Every git invocation names its directory. `git.Cmd.Dir` must be absolute;
  `execgit` panics otherwise.
- Behaviour matches `git stash` exactly unless `README.md` lists the
  difference. A new difference needs a reason, a line in the README's
  "How it differs" section, and, for output, an entry in `stashToStow` or
  `normalize` in `e2e/conformance_test.go`.
- `gofmt` and `go vet` clean; `make check` passes before every commit.
- Commit in small logical steps.

## Test safety (non-negotiable)

A test suite in another repository once wiped a real stash. Tests here must
never touch a repository they did not create.

- Every package whose tests run git, or might, has a `TestMain` that calls
  `gittest.Main(m)`. It scrubs `GIT_*` variables, points git at a throwaway
  global config, pins `HOME`, `TMPDIR`, dates and locale, sets
  `GIT_CEILING_DIRECTORIES` to the run's temporary root, chdirs into a fixture
  repository, refuses to run when that root sits inside another repository,
  and fails the run when the fixture's canaries (a `git stash` entry, an
  uncommitted change, an untracked file) change.
- Create repositories with `gittest.NewRepo(t)` or `Repo.AddWorktree`; they
  live under `t.TempDir()` inside the run's root. Never use a path outside it.
- Run commands through `gittest.Repo` (`Git`, `Stow`, `Run`, `TryRun`), which
  always set the directory. In goroutines use `TryRun`, never `t.Fatal`.
- Never run `git stash` or `git stow` against this repository, from a test or
  by hand while developing. Experiment in a scratch repository under a
  temporary directory.

## Conformance suite

`e2e/conformance_test.go` is the specification. Each scenario sets up two
identical repositories, runs every step as `git stash <args>` in one and
`git stow <args>` in the other, and compares exit status, stdout, stderr, work
tree (file types, modes, contents), index, HEAD, branches and every entry's
commit, tree, parents, message and reflog message. Dates and identities are
pinned, so matching entries are byte-identical commits.

- Every behaviour change or bug fix comes with a scenario.
- A step whose output legitimately differs sets `outputDiffers` to the reason;
  its state is still compared.
- The other test files cover what a differential test cannot: isolation
  between worktrees, concurrency, durability under `git gc` and reflog expiry,
  and the storage layout.
