# c9s

A [K9s](https://k9scli.io/)-inspired terminal UI for browsing and managing
[Claude Code](https://claude.com/claude-code) session history across
multiple isolated profiles (e.g. `claude-work`, `claude-personal`).

Switch contexts, drill from project → session → per-query cost breakdown,
resume a session, view its raw transcript, or delete it — all keyboard-driven,
no mouse required.

## Features

- **Multi-profile contexts** — switch between isolated `CLAUDE_CONFIG_DIR`
  profiles without leaving the TUI.
- **Real usage data** — project and session lists are built by scanning your
  actual `~/.claude*/projects/*.jsonl` transcripts, not mock data.
- **Cost tracking with no hardcoded price table** — session cost/tokens come
  directly from Claude Code's own `cost-state` records, so figures never go
  stale when Anthropic's pricing changes.
- **Per-query cost breakdown** — drill into a session to see an estimated
  cost per individual prompt, derived from the session's own authoritative
  total (no separate pricing table to maintain).
- **Full-screen, k9s-style layout** — fills the terminal, resizes live and
  scrolls long lists; the Session View shows each session's name and its
  last prompt.
- **Session actions** — new (`n`), resume (`r`), view raw log in `$EDITOR` (`v`), delete
  with confirmation (`d`).

## Installation

Requires Go 1.24+.

```sh
git clone <this-repo> ~/c9s
cd ~/c9s
go build -o c9s .
```

To run `c9s` from anywhere, put the binary on your `PATH`, e.g.:

```sh
mkdir -p ~/bin
cp ~/c9s/c9s ~/bin/c9s
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zshrc   # if not already on PATH
source ~/.zshrc
```

## Configuration

c9s reads `~/.config/c9s/config.yaml`. If it doesn't exist, it falls back to
two built-in defaults (`work` → `claude-work`, `personal` → `claude-personal`).

```yaml
current-context: work
contexts:
  work:
    alias: "claude-work"
    base_dir: "~/.claude-work"
    env:
      ANTHROPIC_API_KEY: "env:WORK_API_KEY"   # resolved from your shell env
  personal:
    alias: "claude-personal"
    base_dir: "~/.claude-personal"
```

## Usage

```sh
c9s
```

c9s opens on the all-sessions list, so the session you were last working in is
at the top. Press `s` to switch to the per-project list and back.

### Command bar

| Command | Action |
|---|---|
| `:ctx`  | Switch to the Context View |
| `:proj` | Switch to the Project View |
| `:sess` | All-sessions view across every project, most recently used first (same as `s` from the Project View) |
| `:q`    | Quit |

### Keys

| Key | Action |
|---|---|
| `j` / `↓`, `k` / `↑` | Move selection |
| `/` | Fuzzy-filter the current list |
| `Enter` | Drill into the selected row (Context → Project → Session → Query) |
| `Esc` | Clear filter, or back out one view |
| `r` | Resume the selected session (`claude --resume <id>`) |
| `v` | Open the selected session's raw `.jsonl` in `$EDITOR` |
| `s` | Toggle between the Project View and the all-sessions list (newest first, with a PROJECT column) |
| `n` | Start a new Claude Code session in the selected project's (Project View) or session's (Session View) directory, using the current context's profile |
| `R` | Rename the selected session (a display-only label, stored in `~/.config/c9s/session-names.yaml` — never touches the underlying transcript or its session ID, so `--resume` keeps working) |
| `d` | Delete the selected session (asks for `y`/`n` confirmation) |
| `q` / `Ctrl+C` | Quit |

## Development

See [CLAUDE.md](./CLAUDE.md) for the project's working conventions —
notably that every feature ships with a Gherkin spec under `features/*.feature`
and matching [godog](https://github.com/cucumber/godog) step definitions.

```sh
go build -o c9s .     # build
go vet ./...           # static checks
go test ./...           # run the full BDD suite
go test -run TestConfigFeatures -v .   # run one feature area
```
