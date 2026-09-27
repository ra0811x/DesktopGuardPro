package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFileForHashAllowsRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before.txt")
	if err := os.WriteFile(path, []byte("audit content"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openFileForHash(path)
	if err != nil {
		t.Fatalf("openFileForHash() error = %v", err)
	}
	defer file.Close()

	if err := os.Rename(path, filepath.Join(filepath.Dir(path), "after.txt")); err != nil {
		t.Fatalf("rename while hashing error = %v", err)
	}
}
