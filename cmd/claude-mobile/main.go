package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/chaohaow/claude-mobile-agent/internal/config"
	"github.com/chaohaow/claude-mobile-agent/internal/daemon"
	"github.com/chaohaow/claude-mobile-agent/internal/tmuxctl"
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
	case "start":
		runStart(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: claude-mobile <command>

commands:
  start [DIR]      one-shot: create tmux session running claude in DIR (or cwd),
                   then start the bridge daemon. Config only needs [relay] section.
                   Flags:
                     --permission-mode MODE   passed to claude (default bypassPermissions)
                     --name NAME              override session name (default: basename DIR)
  daemon           start the bridge daemon from a fully specified config
                   (reads ~/.config/claude-mobile/config.toml with [relay] + [session])
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

	runDaemonWithCfg(cfg)
}

func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	permMode := fs.String("permission-mode", "bypassPermissions", "claude --permission-mode value")
	name := fs.String("name", "", "override tmux session name (default: basename of DIR)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	dir := ""
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot determine cwd:", err)
			os.Exit(1)
		}
		dir = wd
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve dir:", err)
		os.Exit(1)
	}
	if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "not a directory: %s\n", absDir)
		os.Exit(1)
	}

	sessName := *name
	if sessName == "" {
		sessName = sanitizeTmuxName(filepath.Base(absDir))
	}
	tmuxSession := "cm-" + sessName

	// Load relay-only config; session will be synthesized.
	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, defaultConfigRelPath)
	cfg, err := config.LoadRelayOnly(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	cfg.Session = config.SessionConfig{
		TmuxTarget: tmuxSession + ":0",
		CWD:        absDir,
		Name:       sessName,
	}

	// Ensure the tmux session exists, spawning claude if not.
	tmux := tmuxctl.New()
	claudeCmd := fmt.Sprintf("claude --permission-mode %s", *permMode)
	if err := tmux.StartSession(tmuxSession, absDir, claudeCmd); err != nil {
		fmt.Fprintln(os.Stderr, "start tmux:", err)
		os.Exit(1)
	}

	fmt.Printf("→ tmux session:  %s   (attach: tmux attach -t %s)\n", tmuxSession, tmuxSession)
	fmt.Printf("→ cwd:           %s\n", absDir)
	fmt.Printf("→ claude cmd:    %s\n", claudeCmd)
	fmt.Printf("→ relay:         %s\n", cfg.Relay.URL)
	fmt.Printf("→ pair_id:       %s\n", cfg.Relay.PairID)
	fmt.Printf("→ device_id:     %s\n", cfg.Relay.DeviceID)
	fmt.Println("→ bridge daemon running; Ctrl-C to stop (tmux session stays alive)")

	runDaemonWithCfg(cfg)
}

func runDaemonWithCfg(cfg *config.Config) {
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

// sanitizeTmuxName drops characters tmux rejects in session names (`.`, `:`,
// whitespace) and trims leading/trailing hyphens.
func sanitizeTmuxName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '.' || r == ':' || r == ' ' || r == '\t':
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}
