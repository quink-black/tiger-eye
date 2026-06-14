package hook

import (
	"testing"

	"github.com/quink/tiger-eye/internal/event"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name    string
		in      codebuddyHook
		wantOK  bool
		want    event.Kind
		wantReq string
		wantMsg string
	}{
		{
			name:    "permission prompt carries request_id",
			in:      codebuddyHook{HookEventName: "Notification", NotificationType: "permission_prompt", RequestID: "abcde"},
			wantOK:  true,
			want:    event.KindPermissionPrompt,
			wantReq: "abcde",
		},
		{
			name:   "idle prompt",
			in:     codebuddyHook{HookEventName: "Notification", NotificationType: "idle_prompt"},
			wantOK: true,
			want:   event.KindIdlePrompt,
		},
		{
			name:   "stop",
			in:     codebuddyHook{HookEventName: "Stop"},
			wantOK: true,
			want:   event.KindStop,
		},
		{
			name:    "post tool use maps to tool_use with tool name as message",
			in:      codebuddyHook{HookEventName: "PostToolUse", ToolName: "Bash"},
			wantOK:  true,
			want:    event.KindToolUse,
			wantReq: "",
			wantMsg: "Bash",
		},
		{
			name:   "subagent stop",
			in:     codebuddyHook{HookEventName: "SubagentStop"},
			wantOK: true,
			want:   event.KindSubagentStop,
		},
		{
			name:   "unknown notification type dropped",
			in:     codebuddyHook{HookEventName: "Notification", NotificationType: "elicitation_dialog"},
			wantOK: false,
		},
		{
			name:   "untracked event dropped",
			in:     codebuddyHook{HookEventName: "PreToolUse"},
			wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, ok := normalize(c.in)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if e.Kind != c.want {
				t.Errorf("kind = %q, want %q", e.Kind, c.want)
			}
			if e.RequestID != c.wantReq {
				t.Errorf("request_id = %q, want %q", e.RequestID, c.wantReq)
			}
			if e.Source != "codebuddy" {
				t.Errorf("source = %q, want codebuddy", e.Source)
			}
			if c.wantMsg != "" && e.Message != c.wantMsg {
				t.Errorf("message = %q, want %q", e.Message, c.wantMsg)
			}
		})
	}
}
