package tmuxctl

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func skipIfNoTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func TestHasSession_Absent(t *testing.T) {
	skipIfNoTmux(t)
	c := New()
	ok, err := c.HasSession("claude-mobile-test-does-not-exist-zz9")
	assert.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSession_Present(t *testing.T) {
	skipIfNoTmux(t)
	name := "claude-mobile-test-present"
	assert.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name).Run())
	defer exec.Command("tmux", "kill-session", "-t", name).Run()

	c := New()
	ok, err := c.HasSession(name)
	assert.NoError(t, err)
	assert.True(t, ok)
}

func TestSendKeys_InjectsText(t *testing.T) {
	skipIfNoTmux(t)
	name := "claude-mobile-test-sendkeys"
	assert.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "cat").Run())
	defer exec.Command("tmux", "kill-session", "-t", name).Run()

	c := New()
	assert.NoError(t, c.SendText(name+":0", "hello world\n"))

	time.Sleep(200 * time.Millisecond)

	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", name+":0").Output()
	assert.NoError(t, err)
	assert.Contains(t, string(out), "hello world")
}
