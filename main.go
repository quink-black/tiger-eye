// Command tiger-eye monitors CodeBuddy CLI agents across local and remote
// machines. It has three subcommands:
//
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

	"github.com/quink/tiger-eye/internal/collect"
	"github.com/quink/tiger-eye/internal/hook"
	"github.com/quink/tiger-eye/internal/node"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "hook":
		err = hook.Run(args)
	case "node":
		err = node.Run(args)
	case "collect":
		err = collect.Run(args)
	case "-h", "--help", "help":
		usage()
		return
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
  tiger-eye hook              read a hook event on stdin and post it to the local node
  tiger-eye node [flags]      run the per-host node daemon
  tiger-eye collect [flags]   run the local collector and TUI dashboard

Run "tiger-eye <subcommand> -h" for subcommand flags.
`)
}
