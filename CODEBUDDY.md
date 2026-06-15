# CODEBUDDY.md

This file provides guidance to CodeBuddy Code when working with code in this repository.

## Build & Test

```bash
# Build (zero CGO, single static binary)
go build -o tiger-eye .

# Cross-compile for remote Linux/arm64
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o tiger-eye-linux-arm64 .

# Run all tests
go test ./...

# Run a single package's tests
go test ./internal/event/...
go test ./internal/hook/...

# Run a specific test with verbose output
go test ./internal/event/... -run TestApply -v
```

No Makefile, no CI configuration. Builds with raw `go build`.

## Architecture

A single Go binary with four subcommands, organized as packages under `internal/`:

```
CodeBuddy Agent --hook--> tiger-eye hook --POST /ingest--> tiger-eye node (ring buffer)
                                                                     |
                                                           (SSH tunnel / loopback)
                                                                     |
tiger-eye collect <--GET /events?since=N&wait=MS--------- tiger-eye node
       |
       +-- Store (AgentState map) --> tui.Source interface --> Bubble Tea TUI
```

### Subcommands (dispatched in `main.go` via `os.Args[1]` switch)

| Subcommand | Package | Runs on | Role |
|---|---|---|---|
| `hook` | `internal/hook` | every agent host | Reads CodeBuddy hook JSON from stdin, normalizes to `event.Event`, POSTs to local node. Always returns exit 0 (errors go to stderr only) so monitoring never blocks the agent. |
| `node` | `internal/node` | every agent host | Buffers events in memory, serves 4 HTTP endpoints (`/healthz`, `/ingest`, `/events`, `/sessions`) on loopback with bearer-token auth. |
| `collect` | `internal/collect` | monitoring host | Pulls from all hosts (loopback, direct HTTP, or `ssh -L` tunnel), folds events into a shared `Store`, drives the TUI. |
| `stand` | `internal/standalone` | single-machine host | Runs node + collect in one process. Auto-creates a local host entry for the embedded node; loads hosts.toml if present to also pull from remote hosts. Quick-start for users who only monitor agents on their own device. |

### Packages

| Package | Purpose | Key types |
|---|---|---|
| `internal/event` | Shared event schema + state machine. Zero external deps. | `Event`, `Kind`, `State`, `Apply(Kind) State`, `Priority(State) int`, `DeriveStale(...)` |
| `internal/config` | Hand-rolled TOML parser for `~/.config/tiger-eye/hosts.toml`. Understands only `[[host]]` tables and flat `key = value`. | `Host`, `Hosts`, `LoadHosts(path)` |
| `internal/hook` | CodeBuddy hook stdin normalization + HTTP POST to node. | `Run(args)`, `normalize(...)`, `post(...)` |
| `internal/node` | Per-host HTTP daemon with ring buffer and long-poll. | `Server`, `New()`, `ListenAndServe()`, `Shutdown()`, `buffer` (channel-close-and-replace broadcast) |
| `internal/collect` | Orchestrator: SSH tunnels, pull loop, per-session state store. | `Store`, `RunWithHosts()`, `puller`, `tunnel`, `AgentState`, `HostStatus` |
| `internal/standalone` | Quick-start mode: node + collect in one process. | `Run(args)` |
| `internal/tui` | Bubble Tea dashboard with lipgloss styling. | `model`, `Source` interface, `Row`, `HostHealth` |

### Key design decisions

- **Pull, not push**: DC servers cannot connect back and often forbid `ssh -R`. Nodes bind loopback only; the collector opens `ssh -L` tunnels to reach them. This works everywhere.
- **No external dependencies for core logic**: Config parsing, HTTP serving, event processing all use stdlib. Only the TUI pulls in `bubbletea` + `lipgloss`. The binary is dependency-light for easy `scp` deployment.
- **Interface boundary at `tui.Source`**: `collect/tuisource.go` implements `tui.Source` on the `Store`. This prevents circular imports between `collect` and `tui` and allows pluggable backends.
- **Broadcast long-poll in `node/buffer.go`**: The buffer's `notify` channel is closed and replaced on each `append()`, waking all long-poll waiters simultaneously with zero per-waiter bookkeeping.
- **Soft errors in hooks**: `internal/hook` always returns nil. Errors go to stderr only, so CodeBuddy's hook mechanism never blocks an agent due to monitoring failures.
- **TUI-safe errors in collector**: Connection errors are recorded on `Store` and rendered in the dashboard footer, never printed to stderr (avoids TUI corruption).

### Event state machine

`event.Apply(State, Kind) -> State` is a pure function mapping the 7 event kinds to 7 lifecycle states:

```
permission_prompt -> waiting_permission
idle_prompt       -> waiting_input
stop              -> done
subagent_stop     -> subagent_done
session_end       -> ended
session_start     -> running
auth_success      -> running (keeps current)
```

`StaleAfter = 2 * time.Minute`. Only `running` can go stale; blocking states (`waiting_permission`, `waiting_input`) and terminal states never degrade.

Dashboard sort order (most urgent first): `waiting_permission > waiting_input > stale > done > subagent_done > running > ended`.

### Config file

Hand-parsed TOML subset at `~/.config/tiger-eye/hosts.toml`. Three host modes:

| Mode | Required fields | Transport |
|---|---|---|
| `local` | `port` | `http://127.0.0.1:<port>` |
| `lan` | `addr`, `port` | `http://<addr>:<port>` (direct, trusted LAN only) |
| `ssh` | `ssh`, `port` | `ssh -L <local>:127.0.0.1:<port>` |

Tokens support `env:VARNAME` indirection to keep secrets out of the file. See `hosts.example.toml`.

### Existing tests

Only `internal/event/` and `internal/hook/` have test files. Both test pure functions with table-driven tests, no I/O or mocks.
