# tiger-eye

Monitor CodeBuddy and Codex CLI agents across your machines in one live
terminal dashboard. Shows which agents are **waiting for permission**,
**waiting for input**, or **done** — so you stop polling tmux panes and SSH
sessions manually.

## Quick start (single machine)

If you only need to monitor agents on your own machine, use `tiger-eye stand`.
It runs the node and dashboard in one process — no config file, no SSH setup.

### 1. Build

```bash
go build -o tiger-eye .
```

### 2. Add hooks to CodeBuddy

Add this to `~/.codebuddy/settings.json` (merge if the file exists):

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

Then run `/hooks` in CodeBuddy to register them.

### 3. Run the dashboard

```bash
tiger-eye stand
```

That's it. You'll see a live table of your active agents. Press `q` to quit.

---

## Multi-machine setup (advanced)

If you want to monitor agents on remote servers, you need one **node** per
machine and a **collector** that pulls from all of them.

See [Why pull, not push?](docs/faq.md) for the architecture rationale.

### 1. On each remote agent host

Copy the binary over:

```bash
scp tiger-eye-linux-arm64 myserver:/usr/local/bin/tiger-eye
```

Pick a shared token and save it (the node reads `$TIGER_EYE_TOKEN` first,
then falls back to `~/.config/tiger-eye/token`):

```bash
export TIGER_EYE_TOKEN='choose-a-long-random-string'
# Or persist it so non-login shells (GUI IDEs, launchd) can find it:
mkdir -p ~/.config/tiger-eye && echo 'choose-a-long-random-string' > ~/.config/tiger-eye/token
```

Start the node (loopback only — safe by default):

```bash
tiger-eye node &
# tiger-eye node listening on 127.0.0.1:47100
```

Wire up the same CodeBuddy hooks as in the single-machine quick start above.
Make sure the agent process can reach the token — either via `TIGER_EYE_TOKEN`
in its environment or the token file.

#### Codex CLI (optional)

For Codex, add hook entries to `~/.codex/config.toml`:

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

### 2. On the monitoring host

Write `~/.config/tiger-eye/hosts.toml` (see
[`hosts.example.toml`](./hosts.example.toml) for all options):

```toml
[[host]]
name  = "local"
mode  = "local"
port  = 47100
token = "env:TE_TOKEN"

[[host]]
name  = "myserver"
mode  = "ssh"
ssh   = "myserver"          # ~/.ssh/config entry or hostname
port  = 47100
token = "env:TE_TOKEN"
```

Export the token:

```bash
export TE_TOKEN='the-shared-token'
```

Run the dashboard:

```bash
tiger-eye collect
```

You'll see a live table, with the most urgent agents at the top:

```
MACHINE      STATE                AGE        CWD
myserver     waiting_permission   3s         /work/build
local        running              12s        /Users/me/proj
```

Press `q` to quit.

---

## Build

```bash
go build -o tiger-eye .

# Cross-compile for remote Linux/arm64:
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o tiger-eye-linux-arm64 .
```

## Subcommands

| Command | Role |
| --- | --- |
| `tiger-eye stand` | node + dashboard in one process (single-machine quick start) |
| `tiger-eye hook` | reads a CodeBuddy hook event on stdin, POSTs to the local node |
| `tiger-eye codex-hook` | reads a Codex hook event on stdin, POSTs to the local node |
| `tiger-eye node` | buffers events, serves the token-authed pull API |
| `tiger-eye collect` | pulls from all hosts, tracks per-session state, draws the TUI |

## Host modes

| `mode` | Required fields | How the collector reaches it |
| --- | --- | --- |
| `local` | `port` | `http://127.0.0.1:<port>` |
| `ssh` | `ssh`, `port` | opens `ssh -N -L <random>:127.0.0.1:<port> <ssh>`, pulls the local end |
| `lan` | `addr`, `port` | `http://<addr>:<port>` directly (trusted LAN only) |

Tokens can be written inline or as `env:VARNAME` to keep secrets out of the
config file.

## States

| State | Meaning | Goes stale? |
| --- | --- | --- |
| `waiting_permission` | agent is blocked on a permission prompt | no |
| `waiting_input` | agent is blocked waiting for your reply | no |
| `running` | recently active | yes, after 2 min of silence |
| `done` | main agent finished | no (terminal) |
| `subagent_done` | a subagent finished | no (terminal) |
| `ended` | session ended | no (terminal) |
| `stale` | running agent went silent >2 min — may have died | — |

Dashboard sort order (most urgent first):
`waiting_permission > waiting_input > stale > done > subagent_done > running > ended`.

## Security

- Every node API call requires `Authorization: Bearer <token>`.
- Nodes bind `127.0.0.1` by default; remote nodes are reachable only through
  your own `ssh -L` tunnel.
- Use `mode = "ssh"` even on LAN unless you specifically want direct HTTP.
- Keep tokens out of the config file with `env:` indirection, or persist them
  to `~/.config/tiger-eye/token` for environments where env vars are unavailable.

## More

- [FAQ](docs/faq.md) — architecture decisions and design rationale.
- [hosts.example.toml](./hosts.example.toml) — all config options with comments.
