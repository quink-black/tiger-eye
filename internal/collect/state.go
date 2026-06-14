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

// HostStatus is the connection health of one configured host, shown as a
// footer in the dashboard. Errors are recorded here rather than printed to
// stderr, which would corrupt the TUI's alternate screen.
type HostStatus struct {
	Name string
	OK   bool
	Err  string // last connection/pull error while not OK
}

// Store holds the latest derived state for every known agent session. It is the
// single source of truth the collector writes and the dashboard reads, so it is
// safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	agents map[string]*AgentState // key: machine\x00session_id
	hosts  map[string]*HostStatus // key: host name
}

func NewStore() *Store {
	return &Store{
		agents: make(map[string]*AgentState),
		hosts:  make(map[string]*HostStatus),
	}
}

// SetHostOK marks a host as connected (clears any prior error).
func (s *Store) SetHostOK(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[name] = &HostStatus{Name: name, OK: true}
}

// SetHostError records a host's latest connection/pull failure.
func (s *Store) SetHostError(name, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[name] = &HostStatus{Name: name, OK: false, Err: msg}
}

// HostStatuses returns the per-host connection health, sorted by name.
func (s *Store) HostStatuses() []HostStatus {
	s.mu.Lock()
	out := make([]HostStatus, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, *h)
	}
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
