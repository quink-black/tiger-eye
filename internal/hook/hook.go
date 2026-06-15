// Package hook implements `tiger-eye hook` and `tiger-eye codex-hook`: each
// reads a hook payload on stdin, normalizes it to the shared event schema,
// and posts it to the local node. Both subcommands must always exit 0 so a
// monitoring failure never blocks the agent.
package hook

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/event"
)

// codebuddyHook is the subset of CodeBuddy's hook stdin JSON we consume. Fields
// absent for a given event simply stay zero.
type codebuddyHook struct {
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	PermissionMode   string `json:"permission_mode"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	RequestID        string `json:"request_id"`
	ToolName         string `json:"tool_name"`
}

// codexHook is the subset of Codex's hook stdin JSON we consume. Fields absent
// for a given event simply stay zero. See
// https://developers.openai.com/codex/hooks for the full schema.
type codexHook struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Model          string `json:"model"`
	TurnID         string `json:"turn_id"`
	PermissionMode string `json:"permission_mode"`
	// SessionStart
	Source string `json:"source"` // "startup", "resume", "clear", "compact"
	// SubagentStart / SubagentStop
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	// Tool events
	ToolName  string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	ToolInput string `json:"tool_input"`
	// UserPromptSubmit
	Prompt string `json:"prompt"`
	// Stop / SubagentStop
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
	// PreCompact / PostCompact
	Trigger string `json:"trigger"`
}

// Run reads a CodeBuddy hook payload on stdin, normalizes it, and posts to the
// node. Errors are logged to stderr but never propagate.
func Run(args []string) error {
	return runHook("hook", args, normalizeCB)
}

// RunCodex reads a Codex hook payload on stdin, normalizes it, and posts to the
// node. Errors are logged to stderr but never propagate.
func RunCodex(args []string) error {
	return runHook("codex-hook", args, normalizeCodexRaw)
}

// runHook is the shared core: parse flags, read stdin, normalize, post.
// name is the subcommand name used in warning messages.
func runHook(name string, args []string, normalizeFunc func(json.RawMessage) (event.Event, bool)) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	port := fs.Int("port", envPort(), "local node port")
	token := fs.String("token", os.Getenv("TIGER_EYE_TOKEN"), "bearer token (or set TIGER_EYE_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		warnf(name, "read stdin: %v", err)
		return nil
	}

	e, ok := normalizeFunc(json.RawMessage(data))
	if !ok {
		return nil
	}

	if err := post(*port, *token, e); err != nil {
		warnf(name, "post to node: %v", err)
	}
	return nil
}

// normalizeCB unmarshals a CodeBuddy hook payload and normalizes it.
func normalizeCB(raw json.RawMessage) (event.Event, bool) {
	var h codebuddyHook
	if err := json.Unmarshal(raw, &h); err != nil {
		warnf("hook", "parse hook json: %v", err)
		return event.Event{}, false
	}
	return normalize(h)
}

// normalizeCodexRaw unmarshals a Codex hook payload and normalizes it.
func normalizeCodexRaw(raw json.RawMessage) (event.Event, bool) {
	var h codexHook
	if err := json.Unmarshal(raw, &h); err != nil {
		warnf("codex-hook", "parse hook json: %v", err)
		return event.Event{}, false
	}
	return normalizeCodex(h)
}

// normalize maps a CodeBuddy hook payload to a tiger-eye event. The second
// return is false for hook events that do not correspond to a tracked state.
func normalize(h codebuddyHook) (event.Event, bool) {
	e := event.Event{
		Source:         "codebuddy",
		Cwd:            h.Cwd,
		SessionID:      h.SessionID,
		TranscriptPath: h.TranscriptPath,
		PermissionMode: h.PermissionMode,
		Message:        h.Message,
		RequestID:      h.RequestID,
		Time:           time.Now().UTC(),
	}

	switch h.HookEventName {
	case "Notification":
		switch h.NotificationType {
		case "permission_prompt":
			e.Kind = event.KindPermissionPrompt
		case "idle_prompt":
			e.Kind = event.KindIdlePrompt
		case "auth_success":
			e.Kind = event.KindAuthSuccess
		default:
			return event.Event{}, false
		}
	case "PostToolUse":
		e.Kind = event.KindToolUse
		if h.ToolName != "" {
			e.Message = h.ToolName
		}
	case "Stop":
		e.Kind = event.KindStop
	case "SubagentStop":
		e.Kind = event.KindSubagentStop
	case "SessionStart":
		e.Kind = event.KindSessionStart
	case "SessionEnd":
		e.Kind = event.KindSessionEnd
	default:
		return event.Event{}, false
	}
	return e, true
}

// normalizeCodex maps a Codex hook payload to a tiger-eye event. The second
// return is false for hook events that do not correspond to a tracked state.
func normalizeCodex(h codexHook) (event.Event, bool) {
	e := event.Event{
		Source:         "codex",
		Cwd:            h.Cwd,
		SessionID:      h.SessionID,
		TranscriptPath: h.TranscriptPath,
		PermissionMode: h.PermissionMode,
		Time:           time.Now().UTC(),
	}

	switch h.HookEventName {
	case "SessionStart":
		e.Kind = event.KindSessionStart
		if h.Source != "" {
			e.Message = h.Source
		}
	case "SubagentStart":
		e.Kind = event.KindSessionStart
		if h.AgentType != "" {
			e.Message = h.AgentType
		}
	case "PermissionRequest":
		e.Kind = event.KindPermissionPrompt
		if h.ToolName != "" {
			e.Message = h.ToolName
		}
	case "UserPromptSubmit":
		// Codex fires this after the user types input (including approving
		// a permission prompt). The agent is now running, not idle.
		e.Kind = event.KindAuthSuccess
	case "PostToolUse":
		e.Kind = event.KindToolUse
		if h.ToolName != "" {
			e.Message = h.ToolName
		}
	case "Stop":
		e.Kind = event.KindStop
	case "SubagentStop":
		e.Kind = event.KindSubagentStop
	default:
		// PreToolUse, PreCompact, PostCompact: no useful state signal.
		return event.Event{}, false
	}
	return e, true
}

func post(port int, token string, e event.Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/ingest", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node returned %s", resp.Status)
	}
	return nil
}

func envPort() int {
	if v := os.Getenv("TIGER_EYE_PORT"); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 {
			return p
		}
	}
	return config.DefaultPort
}

func warnf(name, format string, a ...any) {
	fmt.Fprintf(os.Stderr, "tiger-eye %s: "+format+"\n", append([]any{name}, a...)...)
}
