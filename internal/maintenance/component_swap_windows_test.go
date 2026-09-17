package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentSwapRetainsRollbackFailureAndCanRetry(t *testing.T) {
	pairs := componentSwapPairs(t, t.TempDir())
	activationErr := errors.New("activate denied")
	restoreErr := errors.New("restore denied")
	fail := true
	rename := func(from, to string) error {
		if fail && strings.Contains(from, ".new-") {
			return activationErr
		}
		if fail && strings.Contains(from, ".old-") {
			return restoreErr
		}
		return os.Rename(from, to)
	}
	swap, err := prepareComponentSwap(pairs, "retry", rename)
	if err != nil {
		t.Fatal(err)
	}
	err = swap.Activate()
	if !errors.Is(err, activationErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("incomplete failure chain: %v", err)
	}
	if !strings.Contains(err.Error(), pairs[0].TargetPath+".old-retry") {
		t.Fatalf("missing recovery path: %v", err)
	}
	assertFileContent(t, pairs[0].TargetPath+".old-retry", "old-service")
	// A fresh maintenance attempt must not ignore an unfinished replacement.
	if _, err := PrepareComponentSwap(pairs); err == nil {
		t.Fatal("unfinished replacement was ignored")
	}
	fail = false
	if err := swap.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, pairs[0].TargetPath, "old-service")
}

func TestComponentSwapCanCommitAndRollback(t *testing.T) {
	directory := t.TempDir()
	pairs := componentSwapPairs(t, directory)
	swap, err := prepareComponentSwap(pairs, "commit", os.Rename)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Activate(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, pairs[0].TargetPath, "new-service")
	if err := swap.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pairs[0].TargetPath + ".old-commit"); !os.IsNotExist(err) {
		t.Fatalf("backup still exists: %v", err)
	}

	pairs = componentSwapPairs(t, t.TempDir())
	swap, err = prepareComponentSwap(pairs, "rollback", os.Rename)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := swap.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, pairs[0].TargetPath, "old-service")
}

func TestComponentSwapRollsBackEarlierFilesWhenActivationFails(t *testing.T) {
	directory := t.TempDir()
	pairs := componentSwapPairs(t, directory)
	wantErr := errors.New("sharing violation")
	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 4 {
			return wantErr
		}
		return os.Rename(oldPath, newPath)
	}
	swap, err := prepareComponentSwap(pairs, "failure", rename)
	if err != nil {
		t.Fatal(err)
	}
	err = swap.Activate()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Activate() error = %v, want %v", err, wantErr)
	}
	assertFileContent(t, pairs[0].TargetPath, "old-service")
	assertFileContent(t, pairs[1].TargetPath, "old-ui")
}

func componentSwapPairs(t *testing.T, directory string) []ComponentReplacement {
	t.Helper()
	values := []struct{ name, oldValue, newValue string }{
		{name: "service", oldValue: "old-service", newValue: "new-service"},
		{name: "ui", oldValue: "old-ui", newValue: "new-ui"},
		{name: "agent", oldValue: "old-agent", newValue: "new-agent"},
		{name: "maintenance", oldValue: "old-maintenance", newValue: "new-maintenance"},
	}
	pairs := make([]ComponentReplacement, 0, len(values))
	for _, value := range values {
		target := filepath.Join(directory, value.name+".exe")
		staged := filepath.Join(directory, value.name+".staged.exe")
		if err := os.WriteFile(target, []byte(value.oldValue), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(staged, []byte(value.newValue), 0o700); err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, ComponentReplacement{Name: value.name, TargetPath: target, StagedPath: staged})
	}
	return pairs
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || string(content) != want {
		t.Fatalf("file %q = %q, %v; want %q", path, content, err, want)
	}
}
