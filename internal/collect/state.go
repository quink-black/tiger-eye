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
	Source    string // producing adapter, e.g. "codebuddy", "codex"
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
	mu        sync.Mutex
	agents    map[string]*AgentState // key: machine\x00session_id
	hosts     map[string]*HostStatus // key: host name
	notifiers []Notifier
	catchingUp bool // true during initial replay, suppresses notifications

	// notify is closed and replaced on every state change so any number of
	// readers (the dashboard) can wake on a fresh channel without per-waiter
	// bookkeeping. Mirrors node/buffer.go's broadcast pattern.
	notify chan struct{}
}

func NewStore() *Store {
	return &Store{
		agents:    make(map[string]*AgentState),
		hosts:     make(map[string]*HostStatus),
		notify:    make(chan struct{}),
		catchingUp: true,
	}
}

// SetLive enables notifications. Call after the initial batch of events
// from all hosts has been folded in, so replaying buffered history does
// not trigger spurious alerts. If any agents are already in a blocking
// state at this point, NotifyBlocking is dispatched so the user is alerted
// without flooding say.
func (s *Store) SetLive() {
	s.mu.Lock()
	s.catchingUp = false

	var blocked []AgentState
	var notify []Notifier
	if len(s.notifiers) > 0 {
		for _, a := range s.agents {
			if IsBlocking(a.State) {
				blocked = append(blocked, *a)
			}
		}
		if len(blocked) > 0 {
			// Map iteration is unordered; sort so the coalesced message
			// and bell behavior are deterministic across runs.
			sort.SliceStable(blocked, func(i, j int) bool {
				if blocked[i].Machine != blocked[j].Machine {
					return blocked[i].Machine < blocked[j].Machine
				}
				return blocked[i].SessionID < blocked[j].SessionID
			})
			notify = make([]Notifier, len(s.notifiers))
			copy(notify, s.notifiers)
		}
	}
	s.mu.Unlock()

	for _, n := range notify {
		n.NotifyBlocking(blocked)
	}
}

// AddNotifier registers a notifier to receive state transition callbacks.
// Must be called before the pull loops start.
func (s *Store) AddNotifier(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifiers = append(s.notifiers, n)
}

// Notify returns a channel that closes the next time the store changes. The
// caller selects on it once, then calls Notify again for the next change. This
// lets the dashboard refresh the instant an event lands instead of polling.
func (s *Store) Notify() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notify
}

// wake closes the current notify channel and installs a fresh one. Must be
// called with s.mu held.
func (s *Store) wake() {
	close(s.notify)
	s.notify = make(chan struct{})
}

// SetHostOK marks a host as connected (clears any prior error).
func (s *Store) SetHostOK(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.hosts[name]
	s.hosts[name] = &HostStatus{Name: name, OK: true}
	if prev == nil || !prev.OK {
		s.wake()
	}
}

// SetHostError records a host's latest connection/pull failure.
func (s *Store) SetHostError(name, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.hosts[name]
	s.hosts[name] = &HostStatus{Name: name, OK: false, Err: msg}
	if prev == nil || prev.OK || prev.Err != msg {
		s.wake()
	}
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

	k := key(e.Machine, e.SessionID)
	a := s.agents[k]
	if a == nil {
		a = &AgentState{Machine: e.Machine, SessionID: e.SessionID}
		s.agents[k] = a
	}
	prev := a.State
	a.State = event.Apply(a.State, e.Kind)
	a.LastSeen = e.Time
	if e.Cwd != "" {
		a.Cwd = e.Cwd
	}
	if e.Source != "" {
		a.Source = e.Source
	}
	a.Message = e.Message
	// Track the pending permission request; clear it once the prompt resolves
	// into any other state.
	if e.Kind == event.KindPermissionPrompt {
		a.RequestID = e.RequestID
	} else {
		a.RequestID = ""
	}

	// Snapshot notifier list and agent state while locked, then dispatch
	// outside the mutex so notifiers never block the store.
	var notify []Notifier
	var snapshot AgentState
	next := a.State
	changed := prev != next
	if changed && len(s.notifiers) > 0 && !s.catchingUp {
		notify = make([]Notifier, len(s.notifiers))
		copy(notify, s.notifiers)
		snapshot = *a
	}

	s.wake()
	s.mu.Unlock()

	for _, n := range notify {
		n.NotifyTransition(snapshot, prev, next)
	}
}

// MarkHostSessionsEnded transitions all non-terminal sessions on the given
// machine to ended. Call this when a host becomes unreachable, so the
// dashboard does not permanently show blocking states for dead sessions.
func (s *Store) MarkHostSessionsEnded(machine string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, a := range s.agents {
		if a.Machine != machine {
			continue
		}
		if a.State == event.StateEnded {
			continue
		}
		a.State = event.StateEnded
		a.LastSeen = time.Now()
		changed = true
	}
	if changed {
		s.wake()
	}
}

// Snapshot returns the current agents with time-derived staleness applied,
// sorted by dashboard priority then machine/session for stable rendering.
func (s *Store) Snapshot(now time.Time) []AgentState {
	s.mu.Lock()
	out := make([]AgentState, 0, len(s.agents))
	for _, a := range s.agents {
		v := *a
		if v.State == event.StateEnded {
			continue
		}
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
