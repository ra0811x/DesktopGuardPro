package collector

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"desktopguardpro/internal/domain"
)

type FileBaselineEntry struct {
	Path          string
	Size          int64
	Mode          uint32
	CreatedUTC    int64
	AccessedUTC   int64
	ModifiedUTC   int64
	ContentSHA256 string
	HashStatus    string
}

var walkMonitoringDirectory = filepath.WalkDir

func SnapshotMonitoringFileBaseline(
	ctx context.Context,
	targets []domain.MonitoringTarget,
	exclusions []domain.MonitoringExclusion,
) ([]FileBaselineEntry, error) {
	if err := domain.ValidateMonitoringTargets(targets); err != nil {
		return nil, err
	}
	if err := domain.ValidateMonitoringExclusions(exclusions); err != nil {
		return nil, err
	}
	entries := make(map[string]FileBaselineEntry)
	for _, target := range targets {
		if err := snapshotMonitoringTarget(ctx, target, exclusions, entries); err != nil {
			return nil, err
		}
	}
	result := make([]FileBaselineEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Path < result[right].Path })
	return result, nil
}

func snapshotMonitoringTarget(ctx context.Context, target domain.MonitoringTarget, exclusions []domain.MonitoringExclusion, entries map[string]FileBaselineEntry) error {
	if target.Kind == domain.MonitoringTargetKindFile {
		return snapshotBaselineFile(ctx, target.Path, exclusions, entries)
	}
	return walkMonitoringDirectory(target.Path, func(path string, directory fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			// A child can disappear after its parent directory has been read. That
			// is an ordinary filesystem change and must not invalidate the entire
			// baseline. A missing configured root remains a real baseline failure.
			if os.IsNotExist(walkErr) && path != target.Path {
				return nil
			}
			return walkErr
		}
		if path == target.Path {
			return nil
		}
		if directory.IsDir() && !target.Recursive {
			return filepath.SkipDir
		}
		if directory.IsDir() {
			return nil
		}
		return snapshotBaselineFile(ctx, path, exclusions, entries)
	})
}

func snapshotBaselineFile(ctx context.Context, path string, exclusions []domain.MonitoringExclusion, entries map[string]FileBaselineEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if domain.MatchesMonitoringExclusion(exclusions, path, "") {
		return nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect baseline file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if len(entries) >= maximumDirectorySnapshotEntries {
		return errors.New("file baseline exceeds 100000 entries; narrow the monitored directory")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	contentSHA256, hashStatus := hashFileContent(absPath)
	createdUTC, accessedUTC := fileBaselineTimestamps(info)
	entries[absPath] = FileBaselineEntry{
		Path: absPath, Size: info.Size(), Mode: uint32(info.Mode()), CreatedUTC: createdUTC,
		AccessedUTC: accessedUTC, ModifiedUTC: info.ModTime().UTC().UnixNano(),
		ContentSHA256: contentSHA256, HashStatus: hashStatus,
	}
	return nil
}
