package event

import (
	"sort"
	"testing"
	"time"
)

func TestApply(t *testing.T) {
	cases := []struct {
		cur  State
		kind Kind
		want State
	}{
		{StateRunning, KindPermissionPrompt, StateWaitingPerm},
		{StateRunning, KindIdlePrompt, StateIdle},
		{StateRunning, KindStop, StateDone},
		{StateRunning, KindSubagentStop, StateSubagentDone},
		{StateRunning, KindSessionEnd, StateEnded},
		{StateRunning, KindSessionStart, StateRunning},
		{StateRunning, KindAuthSuccess, StateRunning},
		// tool_use resolves waiting_permission -> running
		{StateWaitingPerm, KindToolUse, StateRunning},
		// tool_use preserves other states
		{StateRunning, KindToolUse, StateRunning},
		{StateIdle, KindToolUse, StateIdle},
		{StateDone, KindToolUse, StateDone},
	}
	for _, c := range cases {
		if got := Apply(c.cur, c.kind); got != c.want {
			t.Errorf("Apply(%s, %q) = %q, want %q", c.cur, c.kind, got, c.want)
		}
	}
}

func TestPriorityOrder(t *testing.T) {
	// waiting_permission must sort ahead of everything; ended last.
	states := []State{StateRunning, StateDone, StateWaitingPerm, StateIdle, StateStale, StateEnded, StateSubagentDone}
	sort.SliceStable(states, func(i, j int) bool {
		return Priority(states[i]) < Priority(states[j])
	})
	want := []State{StateWaitingPerm, StateIdle, StateStale, StateDone, StateSubagentDone, StateRunning, StateEnded}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("priority order = %v, want %v", states, want)
		}
	}
}

func TestDeriveStale(t *testing.T) {
	now := time.Now()
	old := now.Add(-3 * time.Minute)
	fresh := now.Add(-30 * time.Second)

	if !DeriveStale(StateRunning, old, now) {
		t.Error("running + 3m silence should be stale")
	}
	if DeriveStale(StateRunning, fresh, now) {
		t.Error("running + 30s silence should not be stale")
	}
	// Sticky blocking states must never decay to stale, even after a long wait:
	// an agent blocked on the user stays the top-priority alert.
	for _, s := range []State{StateWaitingPerm, StateIdle} {
		if DeriveStale(s, old, now) {
			t.Errorf("blocking state %q must never become stale", s)
		}
	}
	// Terminal states are never stale.
	for _, s := range []State{StateDone, StateEnded, StateSubagentDone} {
		if DeriveStale(s, old, now) {
			t.Errorf("terminal state %q should never be stale", s)
		}
	}
}
