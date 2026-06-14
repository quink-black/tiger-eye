package collect

import (
	"sort"
	"sync"
	"time"

	"github.com/quink/tiger-eye/internal/event"
)

// AgentState is the derived view of one agent session, as shown in the
// dashboard. Keyed internally by (machine, session_id).
type AgentState struct {
	Machine   string
	SessionID string
	Cwd       string
	State     event.State
	Message   string
	RequestID string // non-empty while a permission decision can be relayed
	LastSeen  time.Time
}

// Store holds the latest derived state for every known agent session. It is the
// single source of truth the collector writes and the dashboard reads, so it is
// safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	agents map[string]*AgentState // key: machine\x00session_id
}

func NewStore() *Store {
	return &Store{agents: make(map[string]*AgentState)}
}

func key(machine, session string) string { return machine + "\x00" + session }

// Apply folds one event into the store, advancing the session's state machine
// and refreshing its last-seen time.
func (s *Store) Apply(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(e.Machine, e.SessionID)
	a := s.agents[k]
	if a == nil {
		a = &AgentState{Machine: e.Machine, SessionID: e.SessionID}
		s.agents[k] = a
	}
	a.State = event.Apply(a.State, e.Kind)
	a.LastSeen = e.Time
	if e.Cwd != "" {
		a.Cwd = e.Cwd
	}
	a.Message = e.Message
	// Track the pending permission request; clear it once the prompt resolves
	// into any other state.
	if e.Kind == event.KindPermissionPrompt {
		a.RequestID = e.RequestID
	} else {
		a.RequestID = ""
	}
}

// Snapshot returns the current agents with time-derived staleness applied,
// sorted by dashboard priority then machine/session for stable rendering.
func (s *Store) Snapshot(now time.Time) []AgentState {
	s.mu.Lock()
	out := make([]AgentState, 0, len(s.agents))
	for _, a := range s.agents {
		v := *a
		if event.DeriveStale(v.State, v.LastSeen, now) {
			v.State = event.StateStale
		}
		out = append(out, v)
	}
	s.mu.Unlock()

	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := event.Priority(out[i].State), event.Priority(out[j].State)
		if pi != pj {
			return pi < pj
		}
		if out[i].Machine != out[j].Machine {
			return out[i].Machine < out[j].Machine
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}
