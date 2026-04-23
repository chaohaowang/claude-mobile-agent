package sessionmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EncodeCWDToProjectDir mirrors how Claude Code names project subdirectories
// under ~/.claude/projects: every "/" in the cwd is replaced with "-".
// "/Users/chaohaowang" → "-Users-chaohaowang"
func EncodeCWDToProjectDir(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

// FindActiveJSONL returns the most-recently-modified *.jsonl under the project
// subdir corresponding to cwd. Returns an error if no jsonl is found.
func FindActiveJSONL(projectsRoot, cwd string) (string, error) {
	dir := filepath.Join(projectsRoot, EncodeCWDToProjectDir(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read project dir %s: %w", dir, err)
	}
	var best string
	var bestMod int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > bestMod {
			bestMod = info.ModTime().UnixNano()
			best = filepath.Join(dir, e.Name())
		}
	}
	if best == "" {
		return "", fmt.Errorf("no jsonl in %s", dir)
	}
	return best, nil
}
