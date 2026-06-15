// Package collect implements `tiger-eye collect`: it reads the hosts config,
// runs one supervised puller per host (direct for local/lan, via a persistent
// `ssh -L` tunnel for ssh hosts), folds incoming events into a shared Store,
// and drives the TUI dashboard.
package collect

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/tui"
)

// retryDelay backs off a host whose transport or poll keeps failing, so a dead
// DC tunnel does not spin.
const retryDelay = 5 * time.Second

// Run starts the collector. Flags:
//
//	-hosts   path to hosts.toml (default ~/.config/tiger-eye/hosts.toml)
//	-no-tui  print state changes to stdout instead of running the TUI
func Run(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	hostsPath := fs.String("hosts", "", "path to hosts.toml (default ~/.config/tiger-eye/hosts.toml)")
	noTUI := fs.Bool("no-tui", false, "log to stdout instead of running the TUI")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *hostsPath
	if path == "" {
		p, err := config.DefaultHostsPath()
		if err != nil {
			return err
		}
		path = p
	}
	hosts, err := config.LoadHosts(path)
	if err != nil {
		return fmt.Errorf("load hosts: %w", err)
	}
	if len(hosts.Hosts) == 0 {
		return fmt.Errorf("no hosts configured in %s", path)
	}

	return RunWithHosts(hosts.Hosts, hosts.Notifiers, *noTUI)
}

// RunWithHosts starts the collector using the given host list and notifiers,
// bypassing the hosts.toml file. This lets callers (e.g. the standalone mode)
// inject a synthetic local host without maintaining a config file.
func RunWithHosts(hosts []config.Host, notifiers []config.NotifierConfig, noTUI bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	store := NewStore()

	for _, nc := range notifiers {
		store.AddNotifier(buildNotifier(nc.Type))
	}
	if len(notifiers) == 0 {
		store.AddNotifier(DefaultNotifier())
	}

	ready := make(chan struct{}, len(hosts))
	pending := len(hosts)

	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(h config.Host) {
			defer wg.Done()
			superviseHost(ctx, h, store, ready)
		}(h)
	}

	go func() {
		for range ready {
			pending--
			if pending == 0 {
				store.SetLive()
				return
			}
		}
	}()

	if noTUI {
		runHeadless(ctx, store)
	} else {
		if err := tui.Run(ctx, store); err != nil {
			cancel()
			wg.Wait()
			return err
		}
	}
	cancel()
	wg.Wait()
	return nil
}

// superviseHost keeps a host's transport and poll loop alive, respawning on
// failure until ctx is cancelled. Failures are recorded on the Store (shown in
// the dashboard footer) rather than printed, which would corrupt the TUI's
// alternate screen.
func superviseHost(ctx context.Context, h config.Host, store *Store, ready chan<- struct{}) {
	for ctx.Err() == nil {
		if err := runHost(ctx, h, store, ready); err != nil && ctx.Err() == nil {
			store.SetHostError(h.Name, err.Error())
			store.MarkHostSessionsEnded(h.Name)
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return
			}
		}
	}
}

// runHost establishes the transport for one host and runs its poll loop until
// an error or ctx cancellation. For ssh hosts it owns the tunnel lifecycle.
func runHost(ctx context.Context, h config.Host, store *Store, ready chan<- struct{}) error {
	var baseURL string
	var tun *tunnel

	switch h.Mode {
	case config.ModeLocal:
		baseURL = "http://127.0.0.1:" + strconv.Itoa(h.Port)
	case config.ModeLAN:
		baseURL = "http://" + h.Addr + ":" + strconv.Itoa(h.Port)
	case config.ModeSSH:
		t, url, err := startTunnel(ctx, h)
		if err != nil {
			return err
		}
		tun, baseURL = t, url
		defer tun.cmd.Process.Kill()
	}

	p := newPuller(h, baseURL)

	// If the ssh tunnel dies, surface it as an error to trigger respawn.
	tunErr := make(chan error, 1)
	if tun != nil {
		go func() { tunErr <- tun.wait() }()
	}

	var since uint64
	first := true
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-tunErr:
			return fmt.Errorf("ssh tunnel exited: %v", err)
		default:
		}

		evs, last, err := p.poll(ctx, since)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		store.SetHostOK(h.Name)
		for _, e := range evs {
			if e.Machine == "" {
				e.Machine = h.Name
			}
			store.Apply(e)
		}
		since = last
		if first {
			first = false
			ready <- struct{}{}
		}
	}
}

// runHeadless prints a compact state table whenever the snapshot changes. Used
// for debugging and CI where a TUI cannot run.
func runHeadless(ctx context.Context, store *Store) {
	// Tick only refreshes time-derived staleness; store changes redraw at once.
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var last string
	render := func() {
		snap := store.Snapshot(time.Now())
		cur := ""
		for _, a := range snap {
			msg := a.Message
			if len(msg) > 20 {
				msg = msg[:19] + "…"
			}
			cur += fmt.Sprintf("%-12s %-18s %-20s %-18s %s\n", a.Machine, string(a.State), msg, a.SessionID, a.Cwd)
		}
		if cur != last {
			fmt.Print("\033[H\033[2J")
			fmt.Print(cur)
			last = cur
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-store.Notify():
			render()
		case <-t.C:
			render()
		}
	}
}
