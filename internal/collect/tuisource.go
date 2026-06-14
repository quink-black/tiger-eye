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

// Hosts adapts per-host connection health to tui.HostHealth.
func (s *Store) Hosts() []tui.HostHealth {
	st := s.HostStatuses()
	out := make([]tui.HostHealth, len(st))
	for i, h := range st {
		out[i] = tui.HostHealth{Name: h.Name, OK: h.OK, Err: h.Err}
	}
	return out
}
