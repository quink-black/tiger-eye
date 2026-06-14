// Package config loads tiger-eye's host list and per-node settings. To keep
// the binary dependency-free for easy deployment onto data-center servers, the
// config format is a tiny hand-parsed subset of TOML: only [[host]] tables and
// flat key = "value" / key = number lines are understood.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultPort is the loopback port the node listens on and the collector pulls.
const DefaultPort = 47100

// Mode is how the collector reaches a host.
type Mode string

const (
	// ModeLocal: the node runs on this same machine; pull over loopback.
	ModeLocal Mode = "local"
	// ModeLAN: reach the node directly over the network at Addr:Port.
	ModeLAN Mode = "lan"
	// ModeSSH: the node binds loopback on a remote host; the collector opens an
	// `ssh -L` forward (reverse tunnels are banned in DC environments) and pulls
	// the forwarded local port.
	ModeSSH Mode = "ssh"
)

// Host is one monitored machine.
type Host struct {
	Name  string
	Mode  Mode
	Addr  string // lan mode: network address of the node
	SSH   string // ssh mode: ~/.ssh/config alias (jump host handled by ssh config)
	Port  int    // node port on the host
	Token string // bearer token; resolved value (env: indirection expanded)
}

// Hosts is the parsed hosts.toml.
type Hosts struct {
	Hosts     []Host
	Notifiers []NotifierConfig
}

// NotifierType is the kind of built-in notifier.
type NotifierType string

const (
	NotifierSay  NotifierType = "say"
	NotifierBell NotifierType = "bell"
)

// NotifierConfig is one [[notifier]] table entry.
type NotifierConfig struct {
	Type NotifierType
}

// DefaultHostsPath returns ~/.config/tiger-eye/hosts.toml.
func DefaultHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tiger-eye", "hosts.toml"), nil
}

// LoadHosts parses the hosts file at path. Tokens written as "env:NAME" are
// resolved from the environment so secrets never sit in the config file.
func LoadHosts(path string) (*Hosts, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hs Hosts
	var cur *Host
	var curN *NotifierConfig
	flush := func() {
		if cur != nil {
			hs.Hosts = append(hs.Hosts, *cur)
			cur = nil
		}
		if curN != nil {
			hs.Notifiers = append(hs.Notifiers, *curN)
			curN = nil
		}
	}

	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if raw == "[[host]]" {
			flush()
			cur = &Host{Port: DefaultPort}
			continue
		}
		if raw == "[[notifier]]" {
			flush()
			curN = &NotifierConfig{}
			continue
		}
		if cur == nil && curN == nil {
			return nil, fmt.Errorf("line %d: key outside [[host]] or [[notifier]] table: %q", line, raw)
		}
		key, val, ok := splitKV(raw)
		if !ok {
			return nil, fmt.Errorf("line %d: malformed line: %q", line, raw)
		}
		if curN != nil {
			switch key {
			case "type":
				curN.Type = NotifierType(val)
			default:
				return nil, fmt.Errorf("line %d: unknown notifier key %q", line, key)
			}
			continue
		}
		switch key {
		case "name":
			cur.Name = val
		case "mode":
			cur.Mode = Mode(val)
		case "addr":
			cur.Addr = val
		case "ssh":
			cur.SSH = val
		case "token":
			cur.Token = resolveToken(val)
		case "port":
			p, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: port not a number: %q", line, val)
			}
			cur.Port = p
		default:
			return nil, fmt.Errorf("line %d: unknown key %q", line, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()

	for i := range hs.Hosts {
		if err := validate(&hs.Hosts[i]); err != nil {
			return nil, err
		}
	}
	for i := range hs.Notifiers {
		if err := validateNotifier(&hs.Notifiers[i]); err != nil {
			return nil, err
		}
	}
	return &hs, nil
}

func validate(h *Host) error {
	if h.Name == "" {
		return fmt.Errorf("host with empty name")
	}
	switch h.Mode {
	case ModeLocal:
	case ModeLAN:
		if h.Addr == "" {
			return fmt.Errorf("host %q: lan mode requires addr", h.Name)
		}
	case ModeSSH:
		if h.SSH == "" {
			return fmt.Errorf("host %q: ssh mode requires ssh alias", h.Name)
		}
	default:
		return fmt.Errorf("host %q: unknown mode %q", h.Name, h.Mode)
	}
	return nil
}

func validateNotifier(n *NotifierConfig) error {
	switch n.Type {
	case NotifierSay, NotifierBell:
		return nil
	default:
		return fmt.Errorf("notifier with unknown type %q", n.Type)
	}
}

// splitKV parses `key = value`, stripping surrounding quotes and trailing
// inline comments from the value.
func splitKV(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	val = strings.TrimSpace(s[i+1:])
	if len(val) > 0 && val[0] == '"' {
		// Quoted string: take up to the closing quote, ignore any inline comment.
		if j := strings.IndexByte(val[1:], '"'); j >= 0 {
			val = val[1 : 1+j]
			return key, val, key != ""
		}
		return "", "", false
	}
	// Unquoted: strip inline comment.
	if c := strings.IndexByte(val, '#'); c >= 0 {
		val = strings.TrimSpace(val[:c])
	}
	return key, val, key != ""
}

// resolveToken expands an "env:NAME" indirection to the environment variable's
// value. A plain string is returned unchanged.
func resolveToken(v string) string {
	if name, ok := strings.CutPrefix(v, "env:"); ok {
		return os.Getenv(name)
	}
	return v
}
