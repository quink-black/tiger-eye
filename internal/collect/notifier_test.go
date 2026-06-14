package collect

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/quink/tiger-eye/internal/event"
)

func TestIsBlocking(t *testing.T) {
	tests := []struct {
		state event.State
		want  bool
	}{
		{event.StateWaitingPerm, true},
		{event.StateWaitingInput, true},
		{event.StateRunning, false},
		{event.StateDone, false},
		{event.StateStale, false},
		{event.StateEnded, false},
		{event.StateSubagentDone, false},
	}
	for _, tt := range tests {
		if got := IsBlocking(tt.state); got != tt.want {
			t.Errorf("IsBlocking(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestSayMessage(t *testing.T) {
	a := AgentState{Machine: "dc-1"}
	if got := sayMessage(a, event.StateWaitingPerm); got != "Permission prompt on dc-1" {
		t.Errorf("sayMessage(waiting_permission) = %q, want %q", got, "Permission prompt on dc-1")
	}
	if got := sayMessage(a, event.StateWaitingInput); got != "Idle prompt on dc-1" {
		t.Errorf("sayMessage(waiting_input) = %q, want %q", got, "Idle prompt on dc-1")
	}
}

func TestBellNotifierWritesBell(t *testing.T) {
	n := newBellNotifier()
	a := AgentState{Machine: "dc-1"}

	// Capture stderr.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	n.NotifyTransition(a, event.StateRunning, event.StateWaitingPerm)

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "\a" {
		t.Errorf("bellNotifier wrote %q, want \\a", buf.String())
	}
}

func TestBellNotifierSkipsNonBlocking(t *testing.T) {
	n := newBellNotifier()
	a := AgentState{Machine: "dc-1"}

	// Capture stderr.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	n.NotifyTransition(a, event.StateRunning, event.StateDone)

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "" {
		t.Errorf("bellNotifier wrote %q on non-blocking transition, want empty", buf.String())
	}
}

type recordingNotifier struct {
	calls []transitionCall
}

type transitionCall struct {
	agent AgentState
	prev  event.State
	next  event.State
}

func (r *recordingNotifier) NotifyTransition(agent AgentState, prev, next event.State) {
	r.calls = append(r.calls, transitionCall{agent, prev, next})
}

func TestStoreApplyDispatchesOnTransition(t *testing.T) {
	s := NewStore()
	rec := &recordingNotifier{}
	s.AddNotifier(rec)
	s.SetLive()

	e := event.Event{
		Kind:      event.KindPermissionPrompt,
		Machine:   "box",
		SessionID: "s1",
		Time:      testTime(),
	}
	s.Apply(e)

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 notifier call, got %d", len(rec.calls))
	}
	c := rec.calls[0]
	if c.prev != "" {
		t.Errorf("prev = %q, want empty (new agent)", c.prev)
	}
	if c.next != event.StateWaitingPerm {
		t.Errorf("next = %q, want %q", c.next, event.StateWaitingPerm)
	}
	if c.agent.Machine != "box" {
		t.Errorf("agent.Machine = %q, want %q", c.agent.Machine, "box")
	}
}

func TestStoreApplySuppressesDuringCatchUp(t *testing.T) {
	s := NewStore()
	rec := &recordingNotifier{}
	s.AddNotifier(rec)
	// Store starts in catchingUp mode: notifications are suppressed.

	e := event.Event{
		Kind:      event.KindPermissionPrompt,
		Machine:   "box",
		SessionID: "s1",
		Time:      testTime(),
	}
	s.Apply(e)

	if len(rec.calls) != 0 {
		t.Fatalf("expected 0 notifier calls during catch-up, got %d", len(rec.calls))
	}
	// But the state should still be applied.
	if snap := s.Snapshot(testTime()); len(snap) != 1 || snap[0].State != event.StateWaitingPerm {
		t.Errorf("state not applied during catch-up: %+v", snap)
	}
}

func TestStoreApplyNoDispatchOnSameState(t *testing.T) {
	s := NewStore()
	rec := &recordingNotifier{}
	s.AddNotifier(rec)
	s.SetLive()

	// First event: "" -> running is a real transition.
	e1 := event.Event{Kind: event.KindSessionStart, Machine: "box", SessionID: "s1", Time: testTime()}
	s.Apply(e1)
	// Second event: running -> running (auth_success keeps running), no transition.
	e2 := event.Event{Kind: event.KindAuthSuccess, Machine: "box", SessionID: "s1", Time: testTime()}
	s.Apply(e2)

	if len(rec.calls) != 1 {
		t.Errorf("expected 1 notifier call (initial transition only), got %d", len(rec.calls))
	}
}

func TestStoreApplyMultipleNotifiers(t *testing.T) {
	s := NewStore()
	rec1 := &recordingNotifier{}
	rec2 := &recordingNotifier{}
	s.AddNotifier(rec1)
	s.AddNotifier(rec2)
	s.SetLive()

	e := event.Event{Kind: event.KindIdlePrompt, Machine: "box", SessionID: "s1", Time: testTime()}
	s.Apply(e)

	if len(rec1.calls) != 1 || len(rec2.calls) != 1 {
		t.Errorf("expected each notifier called once, got %d and %d", len(rec1.calls), len(rec2.calls))
	}
}

func testTime() time.Time {
	t, _ := time.Parse(time.RFC3339, "2025-01-01T00:00:00Z")
	return t
}
