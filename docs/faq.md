# FAQ

## Why pull, not push?

Data-center servers often **cannot connect back** to the machine you watch
from and **forbid reverse SSH tunnels** (`ssh -R`). So tiger-eye never has
the agent push events out. Instead, each host runs a small **node** daemon
that buffers its own events on a loopback port, and your local **collector**
pulls from every host:

| Host type | Reachability | Transport |
| --- | --- | --- |
| local | — | loopback |
| LAN device | bidirectional | SSH tunnel (default) or direct HTTP |
| DC server | you SSH in via jump host; it can't reach you, `ssh -R` banned | collector opens `ssh -L` forward (allowed) |

All hosts can open a listening port, so pull works everywhere. Nodes bind
`127.0.0.1` by default, so a DC node is reachable **only** through your
`ssh -L` tunnel — the same secure posture on LAN and DC.
