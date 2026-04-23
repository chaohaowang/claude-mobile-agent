package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/chaohaow/claude-mobile-agent/internal/config"
	"github.com/chaohaow/claude-mobile-agent/internal/daemon"
)

const defaultConfigRelPath = ".config/claude-mobile/config.toml"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("claude-mobile 0.0.1-dev")
	case "daemon":
		runDaemon()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: claude-mobile <command>

commands:
  daemon           start the agent daemon (reads ~/.config/claude-mobile/config.toml)
  version          print version`)
}

func runDaemon() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot find home dir:", err)
		os.Exit(1)
	}
	path := filepath.Join(home, defaultConfigRelPath)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	d := daemon.New(cfg)
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, "daemon:", err)
		os.Exit(1)
	}
}
