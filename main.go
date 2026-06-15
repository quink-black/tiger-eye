// Command tiger-eye monitors CodeBuddy CLI agents across local and remote
// machines. It has four subcommands; a bare invocation defaults to stand:
//
//	tiger-eye          run node + collect in one process (default; quick-start
//	                   for single-machine use). Equivalent to "tiger-eye stand".
//	tiger-eye hook     normalize a CodeBuddy hook event (stdin) and post it to
//	                   the local node daemon.
//	tiger-eye node     run the per-host node daemon: buffer events and serve
//	                   the pull API.
//	tiger-eye collect  run the local collector + TUI dashboard: pull events
//	                   from every configured host and aggregate their state.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/quink/tiger-eye/internal/collect"
	"github.com/quink/tiger-eye/internal/hook"
	"github.com/quink/tiger-eye/internal/node"
	"github.com/quink/tiger-eye/internal/standalone"
)

func main() {
	// Default to stand mode: a bare `tiger-eye` (or one with only flags, e.g.
	// `tiger-eye -port 47200`) runs node + collect in one process. A leading
	// argument that is not a flag selects an explicit subcommand.
	args := os.Args[1:]

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			usage()
			return
		}
	}

	cmd := "stand"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "hook":
		err = hook.Run(args)
	case "node":
		err = node.Run(args)
	case "collect":
		err = collect.Run(args)
	case "stand":
		err = standalone.Run(args)
	default:
		fmt.Fprintf(os.Stderr, "tiger-eye: unknown subcommand %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "tiger-eye %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tiger-eye - monitor CodeBuddy agents across machines

Usage:
  tiger-eye [flags]           run node + collect in one process (default)
  tiger-eye hook              read a hook event on stdin and post it to the local node
  tiger-eye node [flags]      run the per-host node daemon
  tiger-eye collect [flags]   run the local collector and TUI dashboard

Run "tiger-eye -h" or "tiger-eye <subcommand> -h" for flags.
`)
}
