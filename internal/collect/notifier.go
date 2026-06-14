package collect

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/event"
)

// Notifier receives state transition events. Implementations must be safe for
// concurrent use.
type Notifier interface {
	NotifyTransition(agent AgentState, prev, next event.State)
}

// IsBlocking reports whether a state is a blocking state that warrants user
// attention.
func IsBlocking(s event.State) bool {
	return s == event.StateWaitingPerm || s == event.StateWaitingInput
}

// sayNotifier uses macOS `say` to speak a message on blocking state
// transitions. Falls back to terminal bell if `say` is unavailable.
type sayNotifier struct {
	fallback Notifier
}

func newSayNotifier() Notifier {
	n := &sayNotifier{}
	if runtime.GOOS != "darwin" {
		n.fallback = newBellNotifier()
	}
	return n
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
	// Run in a goroutine so the store is never blocked on speech synthesis.
	go func() {
		if err := exec.Command("say", msg).Run(); err != nil {
			// say failed (unusual on macOS), fall back to bell this time.
			fmt.Fprint(os.Stderr, "\a")
		}
	}()
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
