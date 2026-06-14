package node

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Session mirrors the relevant fields of a CodeBuddy PID-registry file at
// ~/.codebuddy/sessions/{pid}.json. It lets the dashboard show sessions that
// exist but have not yet produced an event.
type Session struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	Kind      string `json:"kind"`
	Mode      string `json:"mode"`
	Version   string `json:"version"`
	Hostname  string `json:"hostname"`
}

// readSessions reads the CodeBuddy PID registry. Missing directory or
// unparsable files yield an empty result rather than an error: the registry is
// best-effort context, not authoritative.
func readSessions() []Session {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".codebuddy", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []Session
	for _, ent := range entries {
		if ent.IsDir() || filepath.Ext(ent.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out
}
