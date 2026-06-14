package collect

import (
	"testing"
	"time"

	"github.com/quink/tiger-eye/internal/event"
)

func TestMarkHostSessionsEnded(t *testing.T) {
	now := time.Now()

	makeEvent := func(machine, session string, kind event.Kind, t time.Time) event.Event {
		return event.Event{
			Machine:   machine,
			SessionID: session,
			Kind:      kind,
			Time:      t,
		}
	}

	tests := []struct {
		name     string
		agents   []event.Event
		machine  string
		want     map[string]event.State
	}{
		{
			name: "transitions non-terminal sessions to ended",
			agents: []event.Event{
				makeEvent("m1", "s1", event.KindIdlePrompt, now),
				makeEvent("m1", "s2", event.KindSessionStart, now),
				makeEvent("m1", "s3", event.KindPermissionPrompt, now),
			},
			machine: "m1",
			want: map[string]event.State{
				"m1\x00s1": event.StateEnded,
				"m1\x00s2": event.StateEnded,
				"m1\x00s3": event.StateEnded,
			},
		},
		{
			name: "leaves already-ended sessions as ended",
			agents: []event.Event{
				makeEvent("m1", "s1", event.KindSessionEnd, now),
				makeEvent("m1", "s2", event.KindIdlePrompt, now),
			},
			machine: "m1",
			want: map[string]event.State{
				"m1\x00s1": event.StateEnded,
				"m1\x00s2": event.StateEnded,
			},
		},
		{
			name: "does not affect sessions on other machines",
			agents: []event.Event{
				makeEvent("m1", "s1", event.KindIdlePrompt, now),
				makeEvent("m2", "s2", event.KindIdlePrompt, now),
			},
			machine: "m2",
			want: map[string]event.State{
				"m1\x00s1": event.StateWaitingInput,
				"m2\x00s2": event.StateEnded,
			},
		},
		{
			name: "no sessions for machine is a no-op",
			agents: []event.Event{
				makeEvent("m1", "s1", event.KindIdlePrompt, now),
			},
			machine: "m2",
			want: map[string]event.State{
				"m1\x00s1": event.StateWaitingInput,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore()
			for _, e := range tt.agents {
				s.Apply(e)
			}

			s.MarkHostSessionsEnded(tt.machine)

			snap := s.Snapshot(time.Now())
			got := make(map[string]event.State, len(snap))
			for _, a := range snap {
				k := key(a.Machine, a.SessionID)
				got[k] = a.State
			}

			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("%s: state = %s, want %s", k, got[k], want)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("got %d entries, want %d", len(got), len(tt.want))
			}
		})
	}
}
