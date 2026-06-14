// Package tui renders the live aggregation dashboard. It reads snapshots
// through the Source interface so it does not import the collector (avoiding an
// import cycle) and could later be fed by any state provider.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Row is one agent's display state. It mirrors collect.AgentState but is owned
// by the tui package to keep the dependency one-way.
type Row struct {
	Machine   string
	SessionID string
	Cwd       string
	State     string
	Message   string
	LastSeen  time.Time
}

// HostHealth is one configured host's connection status, shown in the footer.
type HostHealth struct {
	Name string
	OK   bool
	Err  string
}

// Source provides the current sorted snapshot of agent rows and host health.
// Notify returns a channel that closes the next time the underlying state
// changes, letting the dashboard refresh immediately instead of polling.
type Source interface {
	Rows(now time.Time) []Row
	Hosts() []HostHealth
	Notify() <-chan struct{}
}

// Run starts the dashboard, refreshing periodically until ctx is cancelled or
// the user quits with q/Ctrl-C.
func Run(ctx context.Context, src Source) error {
	m := model{src: src}
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// tickMsg drives the periodic refresh that recomputes time-derived display
// values (age column, staleness) even when no new event arrives.
type tickMsg time.Time

// changeMsg fires the instant the Source's state changes, so an agent landing
// in waiting_permission shows up immediately rather than on the next tick.
type changeMsg struct{}

type model struct {
	src   Source
	rows  []Row
	hosts []HostHealth
	w, h  int
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(), m.waitChange())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitChange blocks on the Source's notify channel and resolves to a changeMsg
// the moment state changes. It is not a busy loop: the channel only closes when
// the collector applies an event or a host's status flips.
func (m model) waitChange() tea.Cmd {
	ch := m.src.Notify()
	return func() tea.Msg {
		<-ch
		return changeMsg{}
	}
}

func (m model) refresh() model {
	m.rows = m.src.Rows(time.Now())
	m.hosts = m.src.Hosts()
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case changeMsg:
		// State changed: refresh now and re-arm the waiter for the next change.
		return m.refresh(), m.waitChange()
	case tickMsg:
		// Periodic refresh keeps age/staleness current with no new events.
		return m.refresh(), tick()
	}
	return m, nil
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	stateStyles = map[string]lipgloss.Style{
		"waiting_permission": lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")), // red
		"waiting_input":      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")), // orange
		"stale":              lipgloss.NewStyle().Foreground(lipgloss.Color("244")),            // grey
		"done":               lipgloss.NewStyle().Foreground(lipgloss.Color("46")),             // green
		"subagent_done":      lipgloss.NewStyle().Foreground(lipgloss.Color("40")),
		"running":            lipgloss.NewStyle().Foreground(lipgloss.Color("33")), // blue
		"ended":              lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}
)

func styleState(s string) string {
	if st, ok := stateStyles[s]; ok {
		return st.Render(s)
	}
	return s
}

var (
	okStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("40"))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func (m model) View() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("tiger-eye  ·  agent monitor"))
	b.WriteString("\n\n")

	now := time.Now()
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("waiting for events... (no agents reporting yet)"))
		b.WriteString("\n")
	} else {
		c := columns(m.w, m.rows)
		hdr := fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %s",
			c.machine, "MACHINE", c.state, "STATE", c.age, "AGE",
			c.cwd, "CWD", c.message, "MESSAGE", "SESSION")
		b.WriteString(headerStyle.Render(hdr))
		b.WriteString("\n")
		for _, r := range m.rows {
			age := humanAge(now.Sub(r.LastSeen))
			cwd := truncate(collapseHome(r.Cwd), c.cwd)
			sess := truncate(r.SessionID, c.session)
			msg := truncate(r.Message, c.message)
			// State and message columns are padded as plain text *before*
			// styling: lipgloss emits ANSI escapes that %-*s would miscount,
			// throwing off every column to the right (notably an empty message
			// shifting SESSION left).
			statePad := fmt.Sprintf("%-*s", c.state, r.State)
			statePad = strings.Replace(statePad, r.State, styleState(r.State), 1)
			msgPad := fmt.Sprintf("%-*s", c.message, msg)
			if msg != "" {
				msgPad = strings.Replace(msgPad, msg, dimStyle.Render(msg), 1)
			}
			line := fmt.Sprintf("%-*s %s %-*s %-*s %s %s",
				c.machine, truncate(r.Machine, c.machine), statePad,
				c.age, age, c.cwd, cwd, msgPad,
				dimStyle.Render(sess))
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString(m.hostFooter())
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("sorted by urgency  ·  press q to quit"))
	return b.String()
}

// hostFooter renders a one-line-per-host connection summary. Disconnected hosts
// show their last error (truncated) so failures are visible without corrupting
// the screen the way raw stderr would.
func (m model) hostFooter() string {
	if len(m.hosts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("hosts:"))
	b.WriteString("\n")
	for _, h := range m.hosts {
		if h.OK {
			b.WriteString(fmt.Sprintf("  %s %s\n", okStyle.Render("●"), h.Name))
		} else {
			b.WriteString(fmt.Sprintf("  %s %-12s %s\n",
				errStyle.Render("●"), h.Name, dimStyle.Render(truncate(h.Err, 60))))
		}
	}
	return b.String()
}

// colWidths holds the per-column character budget for one dashboard render.
type colWidths struct {
	machine, state, age, cwd, message, session int
}

// columns sizes the table to its content first, then clamps to the terminal.
//
// Each column starts just wide enough for its longest actual value (header
// included), so short content leaves no dead space — the gripe with the old
// fixed-width layout. Only when the natural widths overflow totalWidth do the
// two flex columns (CWD, MESSAGE) shrink, proportionally and down to a floor,
// to claw back the overflow. STATE/AGE/SESSION stay at their natural size.
func columns(totalWidth int, rows []Row) colWidths {
	const (
		state   = 18 // longest state label: "waiting_permission"
		age     = 5  // "120m", "23h"
		gaps    = 5  // single space between the 6 columns
		minFlex = 12 // floor CWD/MESSAGE shrink to on narrow terminals
		sessLen = 12 // truncated UUID prefix
	)
	// Natural content widths (capped at header label minimums).
	machine := len("MACHINE")
	cwd := len("CWD")
	message := len("MESSAGE")
	session := len("SESSION")
	for _, r := range rows {
		machine = max(machine, len(r.Machine))
		cwd = max(cwd, len(collapseHome(r.Cwd)))
		message = max(message, len(r.Message))
		session = max(session, min(len(r.SessionID), sessLen))
	}

	if totalWidth < 40 {
		totalWidth = 200 // pre-resize default; assume a roomy terminal
	}
	// Shrink only the flex columns if the row would overflow the terminal.
	fixed := machine + state + age + session + gaps
	if over := (fixed + cwd + message) - totalWidth; over > 0 {
		flex := cwd + message
		avail := max(totalWidth-fixed, 2*minFlex)
		cwd = max(cwd*avail/flex, minFlex)
		message = max(avail-cwd, minFlex)
	}
	return colWidths{machine, state, age, cwd, message, session}
}

// collapseHome shortens an absolute path under the user's home directory to a
// leading "~" so the CWD column carries more meaningful path tail.
func collapseHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
