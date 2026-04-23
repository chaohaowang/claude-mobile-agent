package sessionmgr

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncodeCWDToProjectDir(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{"/Users/chaohaowang", "-Users-chaohaowang"},
		{"/tmp/my-project", "-tmp-my-project"},
		{"/a/b/c", "-a-b-c"},
		{"/Users/foo/claude_remote", "-Users-foo-claude-remote"},
		{"/path/with_many_underscores", "-path-with-many-underscores"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, EncodeCWDToProjectDir(c.cwd), c.cwd)
	}
}

func TestFindActiveJSONL_PicksNewest(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "-tmp-demo")
	old := filepath.Join(projDir, "old.jsonl")
	newer := filepath.Join(projDir, "newer.jsonl")
	assert.NoError(t, mkdirAndWrite(old, "x", -3600))
	assert.NoError(t, mkdirAndWrite(newer, "y", -60))

	path, err := FindActiveJSONL(root, "/tmp/demo")
	assert.NoError(t, err)
	assert.Equal(t, newer, path)
}

func TestFindActiveJSONL_NoneReturnsError(t *testing.T) {
	root := t.TempDir()
	_, err := FindActiveJSONL(root, "/nope")
	assert.Error(t, err)
}
