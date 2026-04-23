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
// "Enter" or "C-c". To submit, include a trailing "\n".
func (c *Client) SendText(target, text string) error {
	out, err := exec.Command(c.bin, "send-keys", "-t", target, "-l", text).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
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
