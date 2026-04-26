// Package main is the entry point for the ads daemon.
//
// CLI surface:
//
//	--config <path>   path to config.toml (default: /etc/ads/config.toml)
//	--validate        load + validate config and exit 0/1
//	--version         print version and exit
//
// Per claude/specs/project_build.md §3.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/wso2/api-discovery-server/internal/config"
	"github.com/wso2/api-discovery-server/internal/engine"
)

// Version and Commit are set at build time via -ldflags. See Makefile.
var (
	Version = "dev"
	Commit  = "unknown"
)

func main() {
	var (
		configPath = flag.String("config", "/etc/ads/config.toml", "path to config file")
		validate   = flag.Bool("validate", false, "validate config and exit")
		printVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *printVer {
		fmt.Printf("ads %s (%s)\n", Version, Commit)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config:\n%v\n", err)
		os.Exit(1)
	}
	if *validate {
		fmt.Println("config valid")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "received %v, shutting down\n", sig)
		cancel()
	}()

	if err := engine.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "engine exited with error: %v\n", err)
		os.Exit(1)
	}
}
