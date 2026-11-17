package migrate

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// rename is os.Rename, replaceable so that tests can take the cross-volume path.
var rename = os.Rename

// moveFile moves src to dst, which must not exist: os.Rename where it works, and across volumes a
// copy that keeps mode and mtime, is verified by size and sha256, and only then removes src.
func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	// os.Rename replaces an existing file on every OS; never overwriting is this check's job.
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	err := rename(src, dst)
	if err == nil {
		return nil
	}
	if !isCrossDevice(err) {
		return err
	}
	if err := copyVerified(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// moveDir moves a directory to dst, which must not exist. A rename moves it whole; across volumes
// every file is moved on its own, and the emptied source directories are removed.
func moveDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	err := rename(src, dst)
	if err == nil {
		return nil
	}
	if !isCrossDevice(err) {
		return err
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		return moveFile(path, target)
	})
	if err != nil {
		return err
	}
	return removeEmptyDirs(src)
}

// removeEmptyDirs removes dir and the directories under it, deepest first, as long as they are
// empty. A file anywhere keeps its directory and every parent.
func removeEmptyDirs(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			_ = removeEmptyDirs(filepath.Join(dir, e.Name()))
		}
	}
	return os.Remove(dir)
}

// copyVerified copies a regular file to dst, which is created and must not exist, keeping its mode
// and mtime, then reads dst back and compares size and sha256 with what was read from src.
func copyVerified(src, dst string) (err error) {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// The umask applied to OpenFile's mode; set it exactly.
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	size, sum, err := hashFile(dst)
	if err != nil {
		return err
	}
	if n != info.Size() || size != info.Size() || !bytes.Equal(sum, h.Sum(nil)) {
		return fmt.Errorf("copy of %s to %s does not match the original", src, dst)
	}
	return nil
}

func hashFile(path string) (int64, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, nil, err
	}
	return n, h.Sum(nil), nil
}

// sameContent compares two files by size and sha256.
func sameContent(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}
	_, as, err := hashFile(a)
	if err != nil {
		return false, err
	}
	_, bs, err := hashFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(as, bs), nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
