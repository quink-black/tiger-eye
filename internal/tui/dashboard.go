// Package tui renders the live aggregation dashboard. It reads snapshots
// through the Source interface so it does not import the collector (avoiding an
// import cycle) and could later be fed by any state provider.
package tui

import (
	"context"
	"fmt"
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
type Source interface {
	Rows(now time.Time) []Row
	Hosts() []HostHealth
}

// Run starts the dashboard, refreshing periodically until ctx is cancelled or
// the user quits with q/Ctrl-C.
func Run(ctx context.Context, src Source) error {
	m := model{src: src}
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type tickMsg time.Time

type model struct {
	src   Source
	rows  []Row
	hosts []HostHealth
	w, h  int
}

func (m model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
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
	case tickMsg:
		m.rows = m.src.Rows(time.Now())
		m.hosts = m.src.Hosts()
		return m, tick()
	}
	return m, nil
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	stateStyles = map[string]lipgloss.Style{
		"waiting_permission": lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")), // red
		"idle":               lipgloss.NewStyle().Foreground(lipgloss.Color("214")),            // orange
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
		b.WriteString(headerStyle.Render(fmt.Sprintf("%-12s %-20s %-10s %-28s %s",
			"MACHINE", "STATE", "AGE", "CWD", "SESSION")))
		b.WriteString("\n")
		for _, r := range m.rows {
			age := humanAge(now.Sub(r.LastSeen))
			cwd := truncate(r.Cwd, 28)
			sess := truncate(r.SessionID, 12)
			// State column padded before styling so ANSI codes do not break width.
			statePad := fmt.Sprintf("%-20s", r.State)
			statePad = strings.Replace(statePad, r.State, styleState(r.State), 1)
			line := fmt.Sprintf("%-12s %s %-10s %-28s %s",
				truncate(r.Machine, 12), statePad, age, cwd, dimStyle.Render(sess))
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
