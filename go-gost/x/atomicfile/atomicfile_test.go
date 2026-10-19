package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileReplacesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gost.json")
	if err := os.WriteFile(path, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte(`{"services":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(b) != `{"services":[]}` || info.Mode().Perm() != 0640 {
		t.Fatalf("content %q mode %v", b, info.Mode().Perm())
	}
	if err := WriteFile(filepath.Join(dir, "new.json"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "new.json")); info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode().Perm())
	}
	assertNoTemp(t, dir)
}

// A writer failing half way (as with ENOSPC) must leave the previous file untouched and no
// temporary file behind.
func TestWriteFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gost.json")
	if err := os.WriteFile(path, []byte("previous complete config"), 0600); err != nil {
		t.Fatal(err)
	}
	enospc := errors.New("write gost.json: no space left on device")
	err := Write(path, 0600, func(w io.Writer) error {
		if _, err := w.Write([]byte(`{"services":[{"name":"half`)); err != nil {
			return err
		}
		return enospc
	})
	if !errors.Is(err, enospc) {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "previous complete config" {
		t.Fatalf("old file changed: %q", b)
	}
	assertNoTemp(t, dir)
}

func TestWriteIntoMissingDirectoryFails(t *testing.T) {
	if err := WriteFile(filepath.Join(t.TempDir(), "missing", "x.json"), []byte("x"), 0600); err == nil {
		t.Fatal("expected an error")
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}
