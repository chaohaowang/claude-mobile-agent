package tmuxctl

import (
	"fmt"
	"os/exec"
	"strings"
)

type Client struct {
	bin string
}

func New() *Client {
	return &Client{bin: "tmux"}
}

// HasSession returns true if a tmux session with the given name exists.
// target may be "sessname" or "sessname:window" — we only check the session part.
func (c *Client) HasSession(target string) (bool, error) {
	sess := strings.SplitN(target, ":", 2)[0]
	err := exec.Command(c.bin, "has-session", "-t", sess).Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		// tmux exits 1 when the session does not exist; that's a clean "no"
		if exitErr.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, fmt.Errorf("tmux has-session: %w", err)
}

// SendText sends literal text to a tmux pane. The `-l` flag makes tmux treat
// the argument as a literal string rather than interpreting key names like
// "Enter" or "C-c". Does NOT submit — to submit, call SendLine instead.
func (c *Client) SendText(target, text string) error {
	out, err := exec.Command(c.bin, "send-keys", "-t", target, "-l", text).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
	}
	return nil
}

// SendLine sends text then a real Enter keypress. Use this for submitting
// prompts to a TUI that distinguishes pressed-Enter from pasted-LF (Claude
// Code's readline is one such TUI).
func (c *Client) SendLine(target, text string) error {
	// Strip trailing newlines; we emit Enter as a key event below.
	text = strings.TrimRight(text, "\n")
	if text != "" {
		if err := c.SendText(target, text); err != nil {
			return err
		}
	}
	out, err := exec.Command(c.bin, "send-keys", "-t", target, "Enter").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w: %s", err, string(out))
	}
	return nil
}

// SendCtrlC sends a literal Ctrl-C to interrupt the running program.
func (c *Client) SendCtrlC(target string) error {
	out, err := exec.Command(c.bin, "send-keys", "-t", target, "C-c").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys C-c: %w: %s", err, string(out))
	}
	return nil
}

// PaneInfo describes one tmux pane on the current server.
type PaneInfo struct {
	Target string // "session:window.pane"
	Path   string // pane_current_path
	Cmd    string // pane_current_command (e.g. "claude.exe")
}

// ListPanes enumerates every pane across every tmux session, so the caller can
// discover whether the user already has a session running in some cwd.
func (c *Client) ListPanes() ([]PaneInfo, error) {
	out, err := exec.Command(c.bin, "list-panes", "-a", "-F",
		"#{session_name}:#{window_index}.#{pane_index}\t#{pane_current_path}\t#{pane_current_command}").Output()
	if err != nil {
		// tmux exits non-zero when no server is running; treat as "no panes".
		if _, ok := err.(*exec.ExitError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	var panes []PaneInfo
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		panes = append(panes, PaneInfo{Target: parts[0], Path: parts[1], Cmd: parts[2]})
	}
	return panes, nil
}

// FindClaudePaneAt returns the best tmux target for a pane whose current path
// equals `cwd`, preferring one whose foreground command looks like claude
// (matches "claude" substring). Second arg is true when a match was found.
func (c *Client) FindClaudePaneAt(cwd string) (string, bool) {
	panes, err := c.ListPanes()
	if err != nil || len(panes) == 0 {
		return "", false
	}
	var fallback string
	for _, p := range panes {
		if p.Path != cwd {
			continue
		}
		if strings.Contains(strings.ToLower(p.Cmd), "claude") {
			return p.Target, true
		}
		if fallback == "" {
			fallback = p.Target
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}

// CapturePane returns the visible content of a tmux pane.
func (c *Client) CapturePane(target string) (string, error) {
	out, err := exec.Command(c.bin, "capture-pane", "-p", "-t", target).Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w", err)
	}
	return string(out), nil
}

// StartSession creates a new detached tmux session named `name`, with working
// directory `cwd` and running `command` in its first pane. If a session with
// that name already exists, this is a no-op (returns nil) — callers should
// check HasSession first if they care about the distinction.
func (c *Client) StartSession(name, cwd, command string) error {
	// tmux does not overwrite an existing session; `new-session` returns an
	// error. Short-circuit for the common "already running" case.
	if ok, err := c.HasSession(name); err != nil {
		return err
	} else if ok {
		return nil
	}
	out, err := exec.Command(c.bin,
		"new-session", "-d",
		"-s", name,
		"-c", cwd,
		command,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, string(out))
	}
	return nil
}

// run is the package-level tmux runner used by package-level helpers (e.g.
// ListAllClaudePanes). Tests swap it via SetRunner. The default invokes
// `tmux` on PATH and returns its stdout.
var run = func(args ...string) ([]byte, error) {
	return exec.Command("tmux", args...).Output()
}

// SetRunner overrides the package-level tmux runner. Pass nil to restore the
// default (real `tmux` exec). Intended for tests.
func SetRunner(r func(args ...string) ([]byte, error)) {
	if r == nil {
		run = func(args ...string) ([]byte, error) {
			return exec.Command("tmux", args...).Output()
		}
		return
	}
	run = r
}

// ClaudePane is one tmux pane currently running `claude`.
type ClaudePane struct {
	Target string // e.g. "%3" — usable with `tmux send-keys -t`
	CWD    string // absolute path of the pane's current working dir
}

// ListAllClaudePanes scans every tmux pane on every session and returns
// the ones whose `pane_current_command` is exactly `claude`. cwd comes
// straight from `pane_current_path`.
func ListAllClaudePanes() ([]ClaudePane, error) {
	out, err := run("list-panes", "-a", "-F",
		"#{pane_id}\t#{pane_current_command}\t#{pane_current_path}")
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	var panes []ClaudePane
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] != "claude" {
			continue
		}
		panes = append(panes, ClaudePane{Target: parts[0], CWD: parts[2]})
	}
	return panes, nil
}
