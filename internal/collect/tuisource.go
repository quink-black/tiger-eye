package collect

import (
	"time"

	"github.com/quink/tiger-eye/internal/tui"
)

// Rows adapts the Store to tui.Source so the dashboard can read snapshots
// without importing this package.
func (s *Store) Rows(now time.Time) []tui.Row {
	snap := s.Snapshot(now)
	rows := make([]tui.Row, len(snap))
	for i, a := range snap {
		rows[i] = tui.Row{
			Machine:   a.Machine,
			SessionID: a.SessionID,
			Cwd:       a.Cwd,
			State:     string(a.State),
			Message:   a.Message,
			LastSeen:  a.LastSeen,
		}
	}
	return rows
}
