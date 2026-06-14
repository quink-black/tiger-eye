package collect

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/event"
)

// Notifier receives state transition events. Implementations must be safe for
// concurrent use.
type Notifier interface {
	// NotifyTransition is called when an agent's state changes during live
	// operation (not during initial catch-up replay).
	NotifyTransition(agent AgentState, prev, next event.State)

	// NotifyBlocking is called once when catch-up ends if any agents are
	// already in a blocking state. Implementations should coalesce to avoid
	// flooding the user with alerts.
	NotifyBlocking(agents []AgentState)
}

// IsBlocking reports whether a state is a blocking state that warrants user
// attention.
func IsBlocking(s event.State) bool {
	return s == event.StateWaitingPerm || s == event.StateWaitingInput
}

// sayNotifier uses macOS `say` to speak a message on blocking state
// transitions. Falls back to terminal bell if `say` is unavailable.
// Speech messages are queued so only one `say` runs at a time; overlapping
// speech is unintelligible.
type sayNotifier struct {
	fallback Notifier
	queue    chan string
}

func newSayNotifier() Notifier {
	n := &sayNotifier{
		queue: make(chan string, 16),
	}
	if runtime.GOOS != "darwin" {
		n.fallback = newBellNotifier()
		return n
	}
	go n.speakLoop()
	return n
}

// speakLoop drains the message queue serially, running one `say` at a time.
func (n *sayNotifier) speakLoop() {
	for msg := range n.queue {
		if err := exec.Command("say", msg).Run(); err != nil {
			fmt.Fprint(os.Stderr, "\a")
		}
	}
}

func (n *sayNotifier) NotifyTransition(agent AgentState, prev, next event.State) {
	if n.fallback != nil {
		n.fallback.NotifyTransition(agent, prev, next)
		return
	}
	if !IsBlocking(next) {
		return
	}
	msg := sayMessage(agent, next)
	select {
	case n.queue <- msg:
	default:
		// Queue full; ring the bell so the alert is not silently lost.
		fmt.Fprint(os.Stderr, "\a")
	}
}

func (n *sayNotifier) NotifyBlocking(agents []AgentState) {
	if n.fallback != nil {
		n.fallback.NotifyBlocking(agents)
		return
	}
	if len(agents) == 0 {
		return
	}
	msg := blockingMessage(agents)
	select {
	case n.queue <- msg:
	default:
		fmt.Fprint(os.Stderr, "\a")
	}
}

// blockingMessage coalesces already-blocked agents into a single speech
// message to avoid flooding the user with say calls on startup.
func blockingMessage(agents []AgentState) string {
	switch len(agents) {
	case 1:
		return sayMessage(agents[0], agents[0].State)
	case 2:
		return sayMessage(agents[0], agents[0].State) + ", " +
			strings.ToLower(sayMessage(agents[1], agents[1].State))
	default:
		machines := make([]string, len(agents))
		for i, a := range agents {
			machines[i] = a.Machine
		}
		return fmt.Sprintf("%d agents waiting on %s and %s",
			len(agents), strings.Join(machines[:len(machines)-1], ", "),
			machines[len(machines)-1])
	}
}

func sayMessage(agent AgentState, s event.State) string {
	switch s {
	case event.StateWaitingPerm:
		return fmt.Sprintf("Permission prompt on %s", agent.Machine)
	case event.StateWaitingInput:
		return fmt.Sprintf("Idle prompt on %s", agent.Machine)
	default:
		return fmt.Sprintf("Agent on %s is now %s", agent.Machine, s)
	}
}

// bellNotifier writes a terminal bell character to stderr on blocking state
// transitions. Works on all platforms with a terminal.
type bellNotifier struct{}

func newBellNotifier() Notifier {
	return &bellNotifier{}
}

func (n *bellNotifier) NotifyTransition(agent AgentState, prev, next event.State) {
	if !IsBlocking(next) {
		return
	}
	fmt.Fprint(os.Stderr, "\a")
}

func (n *bellNotifier) NotifyBlocking(agents []AgentState) {
	if len(agents) == 0 {
		return
	}
	fmt.Fprint(os.Stderr, "\a")
}

// DefaultNotifier returns the platform-appropriate default notifier: sayNotifier
// on macOS (which falls back to bell internally), bellNotifier elsewhere.
func DefaultNotifier() Notifier {
	if runtime.GOOS == "darwin" {
		return newSayNotifier()
	}
	return newBellNotifier()
}

// buildNotifier creates a Notifier from a config type.
func buildNotifier(t config.NotifierType) Notifier {
	switch t {
	case config.NotifierSay:
		return newSayNotifier()
	case config.NotifierBell:
		return newBellNotifier()
	default:
		return DefaultNotifier()
	}
}
