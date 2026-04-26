package tmuxctl

import (
	"os/exec"
	"strings"
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

func TestStartSession_CreatesFresh(t *testing.T) {
	skipIfNoTmux(t)
	name := "claude-mobile-test-start-fresh"
	exec.Command("tmux", "kill-session", "-t", name).Run()
	defer exec.Command("tmux", "kill-session", "-t", name).Run()

	dir := t.TempDir()
	c := New()
	// `cat` holds the pane open so has-session returns true afterwards
	assert.NoError(t, c.StartSession(name, dir, "cat"))

	ok, err := c.HasSession(name)
	assert.NoError(t, err)
	assert.True(t, ok)
}

func TestStartSession_NoopIfExists(t *testing.T) {
	skipIfNoTmux(t)
	name := "claude-mobile-test-start-noop"
	assert.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "cat").Run())
	defer exec.Command("tmux", "kill-session", "-t", name).Run()

	c := New()
	dir := t.TempDir()
	// Should NOT error even though the session already exists.
	assert.NoError(t, c.StartSession(name, dir, "cat"))

	// Sanity: still exactly one session with that name.
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	assert.NoError(t, err)
	count := 0
	for _, l := range []byte(out) {
		if l == '\n' {
			count++
		}
	}
	// Count occurrences of `name` in the list output.
	occurrences := 0
	i := 0
	for i < len(out) {
		j := i
		for j < len(out) && out[j] != '\n' {
			j++
		}
		if string(out[i:j]) == name {
			occurrences++
		}
		i = j + 1
	}
	assert.Equal(t, 1, occurrences)
}

func TestListAllClaudePanes(t *testing.T) {
	SetRunner(func(args ...string) ([]byte, error) {
		if got := strings.Join(args, " "); !strings.Contains(got, "list-panes") {
			t.Fatalf("unexpected args: %s", got)
		}
		return []byte(strings.Join([]string{
			"%0\tclaude\t/Users/me/proj-a",
			"%1\tzsh\t/Users/me/proj-a",
			"%2\tclaude\t/Users/me/proj-b",
			"%3\tclaude\t/Users/me/proj-a",
			"%4\tclaude.exe\t/Users/me/proj-c", // some installs report a launcher binary
			"",
		}, "\n")), nil
	})
	defer SetRunner(nil)

	panes, err := ListAllClaudePanes()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(panes) != 4 {
		t.Fatalf("want 4 panes, got %d: %+v", len(panes), panes)
	}
	if panes[0].Target != "%0" || panes[0].CWD != "/Users/me/proj-a" {
		t.Fatalf("first pane: %+v", panes[0])
	}
	if panes[2].Target != "%3" {
		t.Fatalf("third pane: %+v", panes[2])
	}
}
