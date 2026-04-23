package sessionmgr

import (
	"os"
	"path/filepath"
	"time"
)

func mkdirAndWrite(path, content string, mtimeOffsetSec int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	t := time.Now().Add(time.Duration(mtimeOffsetSec) * time.Second)
	return os.Chtimes(path, t, t)
}
