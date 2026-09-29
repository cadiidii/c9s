# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# c9s

A K9s-style TUI for browsing Claude Code session/project history across
multiple profiles. Go + bubbletea/lipgloss. See `features/` for the current
behavior spec.

## BDD is mandatory for every feature

Every feature — new or changed — must have a corresponding Gherkin spec under
`features/*.feature` and matching `godog` step definitions. Do not consider a
feature done until both exist and pass. This applies to bug fixes that change
observable behavior too, not just new features.

Write or update the `.feature` file first (or alongside the first draft of
the implementation), in plain business language — no Go, no internal field
names in the step text itself.

## Test file structure (this is load-bearing, not a style preference)

`godog` step definitions live in `steps_<area>_test.go`, package `main`.
**Each file is fully self-contained:**

```go
func Test<Area>Features(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: init<Area>Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/<area>.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

func init<Area>Scenario(ctx *godog.ScenarioContext) {
	// steps
}
```

**Do not create a shared/central test runner or a shared `InitializeScenario`
that wires multiple areas together.** Every `steps_*_test.go` has its own
`Test...` entrypoint and its own `Paths` pointing only at its own feature
file(s). This is deliberate: when multiple features are being added at once
(see "Fan out subagents" below), each area's file is the *only* place that
area's name appears — no engineer or agent needs to touch a file another one
owns, so there's no Go compile race and no merge collision. `go test ./...`
runs all `Test...` functions in the package regardless of how many there are.

Steps return `error` (idiomatic godog), never `t.Fatal` inside a step func.

## Test hermeticity

- Never touch the real `~/.claude*` or `~/.config/c9s` paths. Override `HOME`
  via `os.Setenv` to a temp dir (`os.MkdirTemp`) in `ctx.Before`, restore the
  original value in `ctx.After`.
- Build synthetic `.jsonl` fixtures in temp dirs rather than reading real
  session history — real transcripts contain the user's actual prompts and
  costs and must never end up in checked-in test fixtures.
- For bubbletea `model` behavior (navigation, key handling): construct the
  `model` struct directly with the fixture state you need and drive it via
  `model.Update(tea.KeyMsg{...})`. Don't run the real background-loading
  `tea.Cmd`s (`loadProjectsCmd`, `loadQueriesCmd`) inside these tests — that
  filesystem-scanning behavior belongs to the session/project parsing
  feature's own tests, not the navigation feature's.

## README.md must stay in sync with anything command-related

Any change that touches a keybinding (`handleKey`'s switch), the `:command`
bar (`handleCommandKey`), CLI usage/flags, or the `config.yaml` schema must
update the corresponding section of `README.md` (Keys table, Command bar
table, Usage, or Configuration example) in the *same* change — not as a
follow-up. Treat a command-related change as incomplete until both the code
and the README reflect it, the same way a feature isn't done until its BDD
spec passes. Before finishing, diff what you touched against `README.md`'s
tables and confirm they still match reality.

## Fan out subagents for parallel feature areas

When adding several independent feature areas in one pass, split the work by
area (one `.feature` + one `steps_<area>_test.go` per agent) and run them in
parallel. Because of the self-contained-file rule above, this is safe: no
shared file means no race. Write the `.feature` file(s) yourself first so the
scope of each parallel task is unambiguous, then delegate the step-definition
implementation. After all agents finish, run `go test ./...` once as the
integration check.

## Running the suite

```sh
go build -o c9s .      # build (the ./c9s binary is gitignored)
go vet ./...           # static checks
go test ./...          # everything
go test -run TestConfigFeatures -v .   # one area
```

A `Test...` entrypoint can cover several `.feature` files (Navigation runs
`navigation` + `session_actions`; Session runs `session_and_project` +
`query_cost`), so a new feature file goes in the `Paths` of the area whose
steps it reuses. To run one scenario, use the subtest name (spaces become underscores):
`go test -run 'TestNavigationFeatures/Escaping_the_Query_View_returns_to_the_Session_View' -v .`

## Architecture

Flat `package main` (no subpackages); one bubbletea `model` in `main.go`.

- `main.go` — the whole TUI: `viewState` (Context → Project → Session →
  Query), `handleKey` / `handleCommandKey` / `handleFilterKey` /
  `handleRenameKey` (input modes), `View()` rendering, and resume/view/delete
  actions. Slow work runs in `tea.Cmd`s (`loadProjectsCmd`, `loadQueriesCmd`)
  that return `projectsLoadedMsg` / `queriesLoadedMsg`.
- `session.go` — `ScanProjects` walks `<base_dir>/projects/*/*.jsonl` and
  `ParseSession` reduces each transcript to a `SessionSummary`. Unparseable
  lines are skipped, not fatal; the scanner buffer is 16MB because assistant
  lines can hold huge thinking blocks.
- `query_cost.go` — per-prompt cost. Transcripts have no per-turn cost, only
  cumulative `cost-state` lines (last one wins), so per-model $/token is
  derived from the final `cost-state` and rescaled so query costs sum exactly
  to the session total. **Do not add a hardcoded price table.**
- `config.go` — `~/.config/c9s/config.yaml` (contexts = named
  `CLAUDE_CONFIG_DIR` profiles) and `session-names.yaml` (display-only
  renames keyed by session ID; never modify transcripts). `env:VAR` values in
  a context's `env` are resolved from the shell at launch.
- Resume (`buildResumeCmd`) tries the context's `alias` as a binary, falls
  back to `claude` with the context env applied, and runs in the session's
  recorded `cwd`. Aliases are usually shell aliases, so the fallback is the
  common path.
