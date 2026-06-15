# tiger-eye

Monitor multiple CodeBuddy and Codex CLI agents across your local machine and
remote SSH hosts. When an agent is **waiting for permission**, **waiting for
input**, or **done**, tiger-eye surfaces it in one live terminal dashboard — so
you stop polling tmux panes and SSH sessions to find which agent is blocked.

```
hook ──▶ tiger-eye hook ──▶ node (:47100 buffer) ◀══ PULL (ssh -L) ══ collector ──▶ TUI
```

## Why pull, not push

Data-center servers often **cannot connect back** to the machine you watch from
and **forbid reverse SSH tunnels** (`ssh -R`). So tiger-eye never has the agent push events
out. Instead, each host runs a small **node** daemon that buffers its own events
on a loopback port, and your local **collector** pulls from every host:

| Host type | Reachability | Transport |
| --- | --- | --- |
| local | — | loopback |
| LAN device | bidirectional | SSH tunnel (default) or direct HTTP |
| DC server | you SSH in via jump host; it can't reach you, `ssh -R` banned | collector opens `ssh -L` forward (allowed) |

All hosts can open a listening port, so pull works everywhere. Nodes bind
`127.0.0.1` by default, so a DC node is reachable **only** through your `ssh -L`
tunnel — the same secure posture on LAN and DC.

## Components

A single static Go binary with five subcommands (zero runtime deps — `scp` it
to any server and run):

| Subcommand | Runs on | Role |
| --- | --- | --- |
| `tiger-eye hook` | every agent host | reads a CodeBuddy hook event on stdin, normalizes it, POSTs to the local node |
| `tiger-eye codex-hook` | every agent host | reads an OpenAI Codex hook event on stdin, normalizes it, POSTs to the local node |
| `tiger-eye node` | every agent host | buffers events, serves the token-authed pull API |
| `tiger-eye collect` | the monitoring host | pulls from all hosts, tracks per-session state, draws the TUI |
| `tiger-eye stand` | single-machine host | runs node + collect in one process for quick-start use |

## Build

```bash
go build -o tiger-eye .

# Cross-compile for a remote Linux/arm64 server (no toolchain needed there):
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o tiger-eye-linux-arm64 .
```

---

## Quick start

### 1. On each agent host (local + every remote machine)

**a. Install the binary.** Build locally and copy it over:

```bash
scp tiger-eye-linux-arm64 myserver:/usr/local/bin/tiger-eye
```

**b. Pick a shared token** (any string). Export it so both the node and the
hook can read it:

```bash
export TIGER_EYE_TOKEN='choose-a-long-random-string'
```

**c. Start the node** (binds loopback only — safe by default):

```bash
tiger-eye node -token "$TIGER_EYE_TOKEN" &
# tiger-eye node listening on 127.0.0.1:47100 (machine=myserver)
```

**d. Wire up CodeBuddy hooks.** Add to that host's
`~/.codebuddy/settings.json` (merge if the file exists):

```json
{
  "hooks": {
    "Notification": [
      { "matcher": "permission_prompt|idle_prompt|auth_success",
        "hooks": [{ "type": "command", "command": "tiger-eye hook", "timeout": 5 }] }
    ],
    "Stop":         [{ "hooks": [{ "type": "command", "command": "tiger-eye hook", "timeout": 5 }] }],
    "SubagentStop": [{ "hooks": [{ "type": "command", "command": "tiger-eye hook", "timeout": 5 }] }],
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "tiger-eye hook", "timeout": 5 }] }],
    "SessionEnd":   [{ "hooks": [{ "type": "command", "command": "tiger-eye hook", "timeout": 5 }] }]
  }
}
```

`tiger-eye hook` reads `TIGER_EYE_TOKEN` and `TIGER_EYE_PORT` (default 47100)
from the environment, so make sure your CodeBuddy agents run with
`TIGER_EYE_TOKEN` exported. Then run `/hooks` in CodeBuddy to register them.

**e. (Optional) Wire up Codex hooks.** Codex uses a TOML config file at
`~/.codex/config.toml`. Add `[[hooks.*]]` sections pointing at
`tiger-eye codex-hook`:

```toml
[[hooks.SessionStart]]
[[hooks.SessionStart.hooks]]
type = "command"
command = "tiger-eye codex-hook"

[[hooks.SubagentStart]]
[[hooks.SubagentStart.hooks]]
type = "command"
command = "tiger-eye codex-hook"

[[hooks.PermissionRequest]]
[[hooks.PermissionRequest.hooks]]
type = "command"
command = "tiger-eye codex-hook"

[[hooks.UserPromptSubmit]]
[[hooks.UserPromptSubmit.hooks]]
type = "command"
command = "tiger-eye codex-hook"

[[hooks.PostToolUse]]
[[hooks.PostToolUse.hooks]]
type = "command"
command = "tiger-eye codex-hook"

[[hooks.Stop]]
[[hooks.Stop.hooks]]
type = "command"
command = "tiger-eye codex-hook"

[[hooks.SubagentStop]]
[[hooks.SubagentStop.hooks]]
type = "command"
command = "tiger-eye codex-hook"
```

`tiger-eye codex-hook` supports the same `TIGER_EYE_TOKEN` and `TIGER_EYE_PORT`
environment variables. Codex sessions appear in the dashboard with a `codex`
source tag next to the machine name.

### 2. On the monitoring host

Pick whichever machine you want to watch from — it can be one of the agent
hosts itself, or a separate one. It just needs SSH access to the others.

**a. Write the hosts file** at `~/.config/tiger-eye/hosts.toml`
(see [`hosts.example.toml`](./hosts.example.toml)):

```toml
[[host]]
name = "local"
mode = "local"
port = 47100
token = "env:TE_TOKEN"   # the local node enforces the token too

[[host]]
name = "lan-box"       # a LAN machine — pulled over an ssh -L tunnel
mode = "ssh"
ssh  = "lan-box"       # an entry in your ~/.ssh/config (or an mDNS name)
port = 47100
token = "env:TE_TOKEN"

[[host]]
name = "dc-1"          # a data-center server reached via jump host
mode = "ssh"
ssh  = "dc-1"
port = 47100
token = "env:TE_TOKEN"
```

**b. Export the token** referenced by `env:`:

```bash
export TE_TOKEN='the-shared-token'
```

**c. Run the dashboard:**

```bash
tiger-eye collect
```

You'll see a live table, most urgent first:

```
MACHINE      STATE                AGE        CWD                          SESSION
lan-box      waiting_permission   3s         /work/build                  a1b2c3d4…
dc-1         waiting_input        1m         /srv/api                     e5f6a7b8…
local        running              12s        /Users/me/proj               90abcdef…
```

Press `q` to quit.

---

## Host modes

| `mode` | Required fields | How the collector reaches it |
| --- | --- | --- |
| `local` | `port` | `http://127.0.0.1:<port>` |
| `ssh` | `ssh`, `port` | opens `ssh -N -L <random>:127.0.0.1:<port> <ssh>`, pulls the local end. Recommended default — the node never leaves loopback. |
| `lan` | `addr`, `port` | `http://<addr>:<port>` directly (convenience; only on a trusted LAN, always with a token) |

Tokens may be written inline or, preferably, as `env:VARNAME` to keep secrets
out of the file.

## States

| State | Meaning | Goes stale? |
| --- | --- | --- |
| `waiting_permission` | agent is blocked on a permission prompt | no — stays the top alert until you act |
| `waiting_input` | agent is blocked waiting for you to respond | no — stays a top alert until you reply |
| `running` | recently active | yes, after 2 min of silence |
| `done` | main agent finished (`Stop`) | no (terminal) |
| `subagent_done` | a subagent finished | no (terminal) |
| `ended` | session ended | no (terminal) |
| `stale` | a *running* agent went silent >2 min — may have died | — |

Dashboard sort order (most urgent first):
`waiting_permission > waiting_input > stale > done > subagent_done > running > ended`.

## Security

- Every node API call requires `Authorization: Bearer <token>`.
- Nodes bind `127.0.0.1` by default; remote nodes are reachable only through
  your own `ssh -L` tunnel (which already requires SSH access).
- Use `mode = "ssh"` even on LAN unless you specifically want direct HTTP.
- Keep tokens out of the config file with `env:` indirection.

## Roadmap (designed for, not yet built)

- **Remote approval**: answer a remote agent's permission prompt (`y`/`n`) from
  the dashboard, relayed back via a CodeBuddy channel. The event schema already
  carries `request_id` and the node API reserves `POST /approve`.
- **More notification sinks**: sound, desktop notifications, phone IM — the
  collector's dispatcher is pluggable; the TUI is just the first sink.
- **Other CLI agents**: the collector is source-agnostic; adapters beyond
  CodeBuddy hooks (log tail, PTY) can feed the same event schema.
