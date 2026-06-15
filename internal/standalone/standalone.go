// Package standalone implements `tiger-eye stand`: it starts the local node
// HTTP server in a background goroutine, then runs the collector pulling from
// loopback plus any hosts found in hosts.toml. If hosts.toml does not exist,
// only the local machine is monitored.
//
// This is the quick-start mode for users who only monitor agents on their own
// device.
package standalone

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/quink/tiger-eye/internal/collect"
	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/node"
)

// Run starts node + collect in a single process.
func Run(args []string) error {
	fs := flag.NewFlagSet("stand", flag.ContinueOnError)
	port := fs.Int("port", config.DefaultPort, "node listen port")
	token := fs.String("token", os.Getenv("TIGER_EYE_TOKEN"), "bearer token (or set TIGER_EYE_TOKEN)")
	machine := fs.String("machine", "", "machine name stamped on events (default: hostname)")
	hostsPath := fs.String("hosts", "", "path to hosts.toml (default ~/.config/tiger-eye/hosts.toml)")
	noTUI := fs.Bool("no-tui", false, "log to stdout instead of running the TUI")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Start the node HTTP server in the background.
	n := node.New(*port, *token, *machine)
	go func() {
		if err := n.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "tiger-eye node: %v\n", err)
		}
	}()

	// Build the local host entry for the node we just started.
	localHost := config.Host{
		Name:  n.Machine(),
		Mode:  config.ModeLocal,
		Port:  *port,
		Token: *token,
	}

	// Load hosts.toml and merge with the local host. When the user passes an
	// explicit -hosts path, a load failure is an error: the intent was clear.
	// When falling back to the default path, a missing or unreadable file is
	// expected (the common single-machine quick-start case), so it is ignored.
	var hosts []config.Host
	var notifiers []config.NotifierConfig

	if *hostsPath != "" {
		h, err := config.LoadHosts(*hostsPath)
		if err != nil {
			return fmt.Errorf("load hosts: %w", err)
		}
		hosts = h.Hosts
		notifiers = h.Notifiers
	} else if p, err := config.DefaultHostsPath(); err == nil {
		if h, err := config.LoadHosts(p); err == nil {
			hosts = h.Hosts
			notifiers = h.Notifiers
		}
	}

	hosts = append(hosts, localHost)

	// RunWithHosts blocks until SIGINT/SIGTERM or the TUI exits.
	runErr := collect.RunWithHosts(hosts, notifiers, *noTUI)

	// Shut down the node after the collector exits.
	_ = n.Shutdown(context.Background())
	return runErr
}
