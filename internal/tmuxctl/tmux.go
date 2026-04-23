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
