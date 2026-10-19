// Package atomicfile replaces files so a reader (or the next agent start) sees either the
// complete old content or the complete new content, never a truncated file: the data is
// written to a temporary file in the same directory, synced, and renamed over the target.
// When anything fails (e.g. ENOSPC) the temporary file is removed and the target is left
// untouched.
package atomicfile

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// WriteFile atomically replaces path with data. Like os.WriteFile, perm only applies when
// the file does not exist yet; an existing file keeps its mode (and owner, when possible).
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	})
}

// Write atomically replaces path with what write produces; see WriteFile.
func Write(path string, perm os.FileMode, write func(io.Writer) error) (err error) {
	dir := filepath.Dir(path)
	mode := perm
	uid, gid := -1, -1
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
		uid, gid = fileOwner(info)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if err != nil {
			if !closed {
				_ = f.Close()
			}
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if uid >= 0 && (uid != os.Getuid() || gid != os.Getgid()) {
		_ = f.Chown(uid, gid) // best effort: only root may give a file away
	}
	if err = write(f); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	closed = true
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	if d, dirErr := os.Open(dir); dirErr == nil {
		_ = d.Sync() // persist the rename; failure only weakens crash durability
		_ = d.Close()
	}
	return nil
}
