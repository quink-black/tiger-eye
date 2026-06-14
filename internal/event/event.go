// Package event defines the normalized event schema shared by the hook, node
// and collector, plus the per-session state machine the dashboard renders.
package event

import "time"

// Kind is the normalized event type. Hook-specific CodeBuddy event names and
// notification types are mapped onto these by the hook normalizer so that the
// node, collector and dashboard never need to know CodeBuddy's wire vocabulary.
type Kind string

const (
	KindPermissionPrompt Kind = "permission_prompt"
	KindIdlePrompt       Kind = "idle_prompt"
	KindAuthSuccess      Kind = "auth_success"
	KindStop             Kind = "stop"
	KindSubagentStop     Kind = "subagent_stop"
	KindSessionStart     Kind = "session_start"
	KindSessionEnd       Kind = "session_end"
)

// Event is one normalized agent state report. It is the single wire format
// between hook -> node -> collector. request_id is carried from day one so the
// phase-2 remote-approval path needs no schema change.
type Event struct {
	// Seq is assigned by the node on ingest. It is monotonic per host and lets
	// the collector pull incrementally and dedup. Zero before ingest.
	Seq uint64 `json:"seq"`

	Kind   Kind   `json:"event"`
	Source string `json:"source"` // producing adapter, e.g. "codebuddy"

	// Machine is the host name the event originated on. The node stamps it when
	// missing so a single hook config works unmodified on every host.
	Machine string `json:"machine"`

	Cwd            string `json:"cwd,omitempty"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Message        string `json:"message,omitempty"`

	// RequestID matches a CodeBuddy channel permission request (5 lowercase
	// letters). Present only on permission prompts that can be relayed.
	RequestID string `json:"request_id,omitempty"`

	Time time.Time `json:"time"`
}

// State is the derived lifecycle state of a single agent session.
type State string

const (
	StateRunning      State = "running"
	StateWaitingPerm  State = "waiting_permission"
	StateIdle         State = "idle"
	StateDone         State = "done"
	StateSubagentDone State = "subagent_done"
	StateEnded        State = "ended"
	StateStale        State = "stale"
)

// priority orders states for the dashboard: lower number sorts first (more
// urgent). stale outranks done because a silently-dead agent is more likely to
// need attention than one that cleanly finished.
var priority = map[State]int{
	StateWaitingPerm:  0,
	StateIdle:         1,
	StateStale:        2,
	StateDone:         3,
	StateSubagentDone: 4,
	StateRunning:      5,
	StateEnded:        6,
}

// Priority returns the dashboard sort key for s. Unknown states sort last.
func Priority(s State) int {
	if p, ok := priority[s]; ok {
		return p
	}
	return 99
}

// Apply maps an incoming event kind onto the next session state. It does not
// handle staleness, which is time-derived by the collector (see DeriveStale).
func Apply(_ State, k Kind) State {
	switch k {
	case KindPermissionPrompt:
		return StateWaitingPerm
	case KindIdlePrompt:
		return StateIdle
	case KindSessionEnd:
		return StateEnded
	case KindStop:
		return StateDone
	case KindSubagentStop:
		return StateSubagentDone
	case KindSessionStart, KindAuthSuccess:
		return StateRunning
	default:
		return StateRunning
	}
}

// StaleAfter is the no-event window after which a non-terminal session is
// considered stale. It matches CodeBuddy's worker heartbeat timeout.
const StaleAfter = 2 * time.Minute

// DeriveStale reports whether a session last updated at lastSeen should show as
// stale given the current time.
//
// Only StateRunning can go stale: a running agent that stops emitting events has
// likely silently died, so surfacing it as stale is useful. Every other state
// is held as-is:
//   - waiting_permission / idle are *sticky blocking* states — an agent waiting
//     for the user stays blocked indefinitely, so demoting it to the lower-
//     priority grey "stale" would bury the most urgent alert. It must keep
//     showing as waiting_permission until the user acts.
//   - done / ended / subagent_done are terminal: their last event is final.
func DeriveStale(s State, lastSeen, now time.Time) bool {
	if s != StateRunning {
		return false
	}
	return now.Sub(lastSeen) > StaleAfter
}
