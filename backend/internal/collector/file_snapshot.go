package collector

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const directorySnapshotTimeout = 5 * time.Second
const maximumDirectorySnapshotEntries = 100000

type fileFingerprint struct {
	size     int64
	modified int64
	mode     fs.FileMode
}

func fingerprint(info fs.FileInfo) fileFingerprint {
	return fileFingerprint{size: info.Size(), modified: info.ModTime().UnixNano(), mode: info.Mode()}
}

func scanDirectory(ctx context.Context, root string, subtree bool) (map[string]fileFingerprint, error) {
	result := make(map[string]fileFingerprint)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if len(result) >= maximumDirectorySnapshotEntries {
			return errors.New("directory snapshot exceeds 100000 entries; narrow the monitored directory")
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = fingerprint(info)
		// WalkDir never follows symlinks/junctions into another tree.
		if entry.IsDir() && !subtree {
			return filepath.SkipDir
		}
		return nil
	})
	return result, err
}

func (collector *DirectoryCollector) reconcileDirectory(ctx context.Context, sink Sink, reason string) error {
	scanContext, cancel := context.WithTimeout(ctx, directorySnapshotTimeout)
	defer cancel()
	next, err := scanDirectory(scanContext, collector.root, collector.watchSubtree)
	if err != nil {
		return fmt.Errorf("%s directory snapshot: %w", reason, err)
	}
	if collector.targetFile != "" {
		for path := range next {
			if !collector.matchesTarget(path) {
				delete(next, path)
			}
		}
	}
	if collector.knownFiles == nil {
		collector.knownFiles = next
		return nil
	}
	paths := make([]string, 0, len(next)+len(collector.knownFiles))
	for path := range next {
		paths = append(paths, path)
	}
	for path := range collector.knownFiles {
		if _, exists := next[path]; !exists {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := scanContext.Err(); err != nil {
			return err
		}
		before, hadBefore := collector.knownFiles[path]
		after, hasAfter := next[path]
		action := ""
		switch {
		case !hadBefore && hasAfter:
			action = "file_created"
		case hadBefore && !hasAfter:
			action = "file_deleted"
		case before != after:
			action = "file_modified"
		}
		if action != "" {
			if err := collector.emitRecord(scanContext, sink, action, path, "", collector.now().UTC(), reason); err != nil {
				return err
			}
		}
	}
	collector.knownFiles = next
	return nil
}

func (collector *DirectoryCollector) rememberNotification(action, relative, oldRelative string) {
	if collector.knownFiles == nil {
		return
	}
	relative = filepath.Clean(relative)
	removeTree := func(path string) {
		for known := range collector.knownFiles {
			if known == path || strings.HasPrefix(known, path+string(filepath.Separator)) {
				delete(collector.knownFiles, known)
			}
		}
	}
	if action == "file_deleted" || action == "file_rename_old_name" {
		removeTree(relative)
		return
	}
	if action == "file_renamed" {
		oldRelative = filepath.Clean(oldRelative)
		moved := map[string]fileFingerprint{}
		for known, entry := range collector.knownFiles {
			if known == oldRelative || strings.HasPrefix(known, oldRelative+string(filepath.Separator)) {
				moved[relative+strings.TrimPrefix(known, oldRelative)] = entry
			}
		}
		removeTree(oldRelative)
		for path, entry := range moved {
			collector.knownFiles[path] = entry
		}
	}
	if info, err := os.Lstat(filepath.Join(collector.root, relative)); err == nil {
		collector.knownFiles[relative] = fingerprint(info)
	}
}
