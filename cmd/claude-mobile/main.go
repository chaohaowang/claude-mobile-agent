package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	qrterminal "github.com/mdp/qrterminal/v3"

	"github.com/chaohaowang/claude-mobile-agent/internal/daemon"
	"github.com/chaohaowang/claude-mobile-agent/internal/host"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	os.Args = append(os.Args[:1], os.Args[2:]...)

	switch sub {
	case "daemon":
		runDaemon()
	case "pair":
		runPair()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", sub)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `claude-mobile — Mac-wide bridge between tmux/Claude Code and the iPhone web client.

Usage:
  claude-mobile daemon       Run the bridge. Discovers all tmux panes running claude.
  claude-mobile pair         Print the QR + URL for binding an iPhone to this Mac.
  claude-mobile help         This message.

Config: ~/.config/claude-mobile/host.toml (auto-generated on first run).`)
}

func hostConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "claude-mobile", "host.toml")
}

func loadHostConfig() host.Config {
	path := hostConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatalf("mkdir host config dir: %v", err)
	}
	cfg, err := host.LoadOrGenerate(path)
	if err != nil {
		log.Fatalf("load host config: %v", err)
	}
	return cfg
}

func runDaemon() {
	flag.CommandLine = flag.NewFlagSet("daemon", flag.ExitOnError)
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	cfg := loadHostConfig()
	log.Printf("starting daemon: host_id=%s relay=%s", cfg.HostID, cfg.RelayURL)
	d := daemon.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("shutdown")
		cancel()
	}()
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("daemon: %v", err)
	}
}

func runPair() {
	flag.CommandLine = flag.NewFlagSet("pair", flag.ExitOnError)
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	cfg := loadHostConfig()
	pairURL := fmt.Sprintf("%s/?host=%s", cfg.PublicURL, cfg.HostID)
	fmt.Printf("host_id:  %s\n", cfg.HostID)
	fmt.Printf("url:      %s\n", pairURL)
	fmt.Println("──────────────────────────────────────")
	qrterminal.GenerateHalfBlock(pairURL, qrterminal.L, os.Stdout)
	fmt.Println("──────────────────────────────────────")
	fmt.Println("Scan with iPhone camera; the URL opens the web client pre-bound to this Mac.")
}
