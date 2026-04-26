// Package main is the entry point for the ads daemon.
//
// Round 1 (Foundation) replaces this placeholder with the full lifecycle
// described in claude/specs/project_build.md §3.
package main

import (
	"flag"
	"fmt"
)

// Version is set at build time via -ldflags. See Makefile.
var Version = "dev"

// Commit is set at build time via -ldflags. See Makefile.
var Commit = "unknown"

func main() {
	var (
		configPath = flag.String("config", "/etc/ads/config.toml", "path to config file")
		validate   = flag.Bool("validate", false, "validate config and exit")
		version    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("ads %s (%s)\n", Version, Commit)
		return
	}

	// TODO(round-1): wire config.Load + engine.Run.
	_ = configPath
	_ = validate
	fmt.Println("ads daemon: scaffold only — implement Round 1 to enable")
}
