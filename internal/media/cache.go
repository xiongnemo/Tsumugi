package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Cache struct {
	Dir string
}

func NewCache(dir string) Cache {
	return Cache{Dir: dir}
}

func (c Cache) Path(messageID, fileName, mimeType string) string {
	return filepath.Join(c.Dir, CacheName(messageID, fileName, mimeType))
}

func (c Cache) Ensure() error {
	return os.MkdirAll(c.Dir, 0o700)
}

func OpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
