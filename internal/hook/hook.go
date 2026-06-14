// Package hook implements `tiger-eye hook`: it reads a CodeBuddy hook payload
// on stdin, normalizes it to the shared event schema, and posts it to the local
// node. It is invoked by CodeBuddy `command` hooks and must always exit 0 so a
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
}

// Run reads stdin, normalizes, and posts to the node. Errors are logged to
// stderr but never propagate: Run always returns nil so CodeBuddy sees exit 0.
func Run(args []string) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	port := fs.Int("port", envPort(), "local node port")
	token := fs.String("token", os.Getenv("TIGER_EYE_TOKEN"), "bearer token (or set TIGER_EYE_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		warn("read stdin: %v", err)
		return nil
	}

	var h codebuddyHook
	if err := json.Unmarshal(data, &h); err != nil {
		warn("parse hook json: %v", err)
		return nil
	}

	e, ok := normalize(h)
	if !ok {
		// Event we do not track; nothing to report.
		return nil
	}

	if err := post(*port, *token, e); err != nil {
		warn("post to node: %v", err)
	}
	return nil
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

func warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "tiger-eye hook: "+format+"\n", a...)
}
