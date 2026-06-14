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
	calls         []transitionCall
	blockingCalls [][]AgentState
}

type transitionCall struct {
	agent AgentState
	prev  event.State
	next  event.State
}

func (r *recordingNotifier) NotifyTransition(agent AgentState, prev, next event.State) {
	r.calls = append(r.calls, transitionCall{agent, prev, next})
}

func (r *recordingNotifier) NotifyBlocking(agents []AgentState) {
	cp := make([]AgentState, len(agents))
	copy(cp, agents)
	r.blockingCalls = append(r.blockingCalls, cp)
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

func TestNotifyBlockingSayCoalescesSingle(t *testing.T) {
	agents := []AgentState{
		{Machine: "m1", State: event.StateWaitingInput},
	}
	msg := blockingMessage(agents)
	want := "Idle prompt on m1"
	if msg != want {
		t.Errorf("blockingMessage(1 agent) = %q, want %q", msg, want)
	}
}

func TestNotifyBlockingSayCoalescesTwo(t *testing.T) {
	agents := []AgentState{
		{Machine: "m1", State: event.StateWaitingPerm},
		{Machine: "m2", State: event.StateWaitingInput},
	}
	msg := blockingMessage(agents)
	want := "Permission prompt on m1, idle prompt on m2"
	if msg != want {
		t.Errorf("blockingMessage(2 agents) = %q, want %q", msg, want)
	}
}

func TestNotifyBlockingSayCoalescesMany(t *testing.T) {
	agents := []AgentState{
		{Machine: "m1", State: event.StateWaitingInput},
		{Machine: "m2", State: event.StateWaitingInput},
		{Machine: "m3", State: event.StateWaitingPerm},
	}
	msg := blockingMessage(agents)
	want := "3 agents waiting on m1, m2 and m3"
	if msg != want {
		t.Errorf("blockingMessage(3 agents) = %q, want %q", msg, want)
	}
}

func TestNotifyBlockingBellSingleBell(t *testing.T) {
	n := newBellNotifier()
	agents := []AgentState{
		{Machine: "m1", State: event.StateWaitingInput},
		{Machine: "m2", State: event.StateWaitingPerm},
	}

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	n.NotifyBlocking(agents)

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "\a" {
		t.Errorf("bellNotifier.NotifyBlocking wrote %q, want single \\a", buf.String())
	}
}

func TestNotifyBlockingBellNoAgents(t *testing.T) {
	n := newBellNotifier()

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	n.NotifyBlocking(nil)

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.String() != "" {
		t.Errorf("bellNotifier.NotifyBlocking(nil) wrote %q, want empty", buf.String())
	}
}

func TestSetLiveNotifiesExistingBlockedAgents(t *testing.T) {
	s := NewStore()
	rec := &recordingNotifier{}
	s.AddNotifier(rec)

	// During catch-up, apply events that leave agents in blocking states.
	s.Apply(event.Event{Kind: event.KindPermissionPrompt, Machine: "m1", SessionID: "s1", Time: testTime()})
	s.Apply(event.Event{Kind: event.KindIdlePrompt, Machine: "m2", SessionID: "s2", Time: testTime()})

	// No notifications during catch-up.
	if len(rec.calls) != 0 {
		t.Fatalf("expected 0 transition calls during catch-up, got %d", len(rec.calls))
	}
	if len(rec.blockingCalls) != 0 {
		t.Fatalf("expected 0 blocking calls during catch-up, got %d", len(rec.blockingCalls))
	}

	s.SetLive()

	if len(rec.blockingCalls) != 1 {
		t.Fatalf("expected 1 NotifyBlocking call, got %d", len(rec.blockingCalls))
	}
	blocked := rec.blockingCalls[0]
	if len(blocked) != 2 {
		t.Fatalf("expected 2 blocked agents, got %d", len(blocked))
	}
	// SetLive collects from a map, so it must sort for deterministic output.
	if blocked[0].Machine != "m1" || blocked[1].Machine != "m2" {
		t.Errorf("blocked agents not sorted by machine: got %q, %q",
			blocked[0].Machine, blocked[1].Machine)
	}
}

func TestSetLiveNoNotifyWhenNoBlockedAgents(t *testing.T) {
	s := NewStore()
	rec := &recordingNotifier{}
	s.AddNotifier(rec)

	// Agent in non-blocking state.
	s.Apply(event.Event{Kind: event.KindSessionStart, Machine: "m1", SessionID: "s1", Time: testTime()})

	s.SetLive()

	if len(rec.blockingCalls) != 0 {
		t.Errorf("expected 0 NotifyBlocking calls, got %d", len(rec.blockingCalls))
	}
}
