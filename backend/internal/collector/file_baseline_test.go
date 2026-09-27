package collector

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"desktopguardpro/internal/domain"
)

func TestSnapshotMonitoringFileBaselineCapturesRegularFilesWithinConfiguredScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "nested.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := SnapshotMonitoringFileBaseline(context.Background(), []domain.MonitoringTarget{{
		Path: root, Kind: domain.MonitoringTargetKindDirectory, Recursive: false,
	}}, nil)
	if err != nil {
		t.Fatalf("SnapshotMonitoringFileBaseline() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Path != filepath.Join(root, "report.txt") || entries[0].HashStatus != "available" || entries[0].ContentSHA256 == "" {
		t.Fatalf("baseline entries = %#v", entries)
	}
	if entries[0].CreatedUTC == 0 || entries[0].AccessedUTC == 0 || entries[0].ModifiedUTC == 0 || entries[0].Mode == 0 {
		t.Fatalf("baseline metadata is incomplete: %#v", entries[0])
	}
}

func TestSnapshotMonitoringFileBaselineIgnoresNestedPathThatDisappearsDuringWalk(t *testing.T) {
	root := t.TempDir()
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	disappeared := filepath.Join(root, "temporary", "vanished.txt")
	previousWalk := walkMonitoringDirectory
	walkMonitoringDirectory = func(path string, visit fs.WalkDirFunc) error {
		if err := visit(path, fs.FileInfoToDirEntry(rootInfo), nil); err != nil {
			return err
		}
		return visit(disappeared, nil, &fs.PathError{Op: "open", Path: disappeared, Err: os.ErrNotExist})
	}
	t.Cleanup(func() { walkMonitoringDirectory = previousWalk })

	entries, err := SnapshotMonitoringFileBaseline(context.Background(), []domain.MonitoringTarget{{
		Path: root, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, nil)
	if err != nil {
		t.Fatalf("SnapshotMonitoringFileBaseline() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("baseline entries = %#v, want none", entries)
	}
}
