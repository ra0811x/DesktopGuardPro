package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

var ErrDirectoryChangeOverflow = errors.New("directory change buffer overflow")

type FileChange struct {
	Action       uint32
	RelativePath string
}

type FileChangePayload struct {
	Reconciliation    string `json:"reconciliation,omitempty"`
	Root              string `json:"root"`
	Volume            string `json:"volume"`
	Path              string `json:"path"`
	OldPath           string `json:"oldPath,omitempty"`
	PreviousSize      *int64 `json:"previousSize,omitempty"`
	CurrentSize       *int64 `json:"currentSize,omitempty"`
	ContentSHA256     string `json:"contentSha256,omitempty"`
	ContentHashStatus string `json:"contentHashStatus,omitempty"`
}

type directoryWatch func(
	ctx context.Context,
	root string,
	watchSubtree bool,
	handle func([]FileChange) error,
) error

type DirectoryCollector struct {
	exclusions       []domain.MonitoringExclusion
	knownFiles       map[string]fileFingerprint
	pendingRename    *FileChange
	pendingRenameUTC time.Time
	root             string
	removableVolume  bool
	targetFile       string
	watchSubtree     bool
	watch            directoryWatch
	now              func() time.Time
	hashQueue        *FileHashQueue
	policy           domain.FileAuditPolicy
}

func NewDirectoryCollector(root string, watchSubtree bool) (*DirectoryCollector, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("directory collector root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve directory collector root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect directory collector root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("directory collector root must be a directory")
	}
	return newDirectoryCollector(absRoot, watchSubtree, watchWindowsDirectory, time.Now)
}

func NewFileCollector(path string) (*DirectoryCollector, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("file collector path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve file collector path: %w", err)
	}
	root := filepath.Dir(absPath)
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect file collector directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("file collector directory must be a directory")
	}
	collector, err := newDirectoryCollector(root, false, watchWindowsDirectory, time.Now)
	if err != nil {
		return nil, err
	}
	collector.targetFile = filepath.Base(absPath)
	return collector, nil
}

func newDirectoryCollector(
	root string,
	watchSubtree bool,
	watch directoryWatch,
	now func() time.Time,
) (*DirectoryCollector, error) {
	if watch == nil || now == nil {
		return nil, errors.New("directory collector dependency is required")
	}
	return &DirectoryCollector{
		root: root, watchSubtree: watchSubtree, watch: watch, now: now,
		policy: domain.DefaultMonitoringPolicy().File,
	}, nil
}

func (collector *DirectoryCollector) SetAuditPolicy(policy domain.FileAuditPolicy) {
	collector.policy = policy
}

func (collector *DirectoryCollector) Name() string {
	if collector.targetFile != "" {
		return "windows_file_changes:" + strings.ToLower(filepath.Join(collector.root, collector.targetFile))
	}
	return "windows_directory_changes:" + strings.ToLower(collector.root)
}

func (collector *DirectoryCollector) Run(ctx context.Context, sink Sink) error {
	defer collector.closeHashQueue()
	collector.pendingRename = nil
	if err := collector.reconcileDirectory(ctx, sink, "baseline_or_restart"); err != nil {
		_ = collector.emitHealth(ctx, sink, err)
		return err
	}
	watchStarted := false
	err := collector.watch(ctx, collector.root, collector.watchSubtree, func(changes []FileChange) error {
		if !watchStarted {
			watchStarted = true
			if len(changes) == 0 {
				return collector.reconcileDirectory(ctx, sink, "watch_start")
			}
		}
		return collector.emitChanges(ctx, sink, changes)
	})
	finalContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	flushErr := collector.flushRename(finalContext, sink)
	reason := "final"
	if collector.removableVolume && err != nil && ctx.Err() == nil {
		_ = collector.emitRemovableVolumeUnavailable(finalContext, sink, err)
	}
	if errors.Is(err, ErrDirectoryChangeOverflow) {
		_ = collector.emitHealth(finalContext, sink, err)
		reason = "overflow"
	}
	scanErr := collector.reconcileDirectory(finalContext, sink, reason)
	if scanErr != nil {
		_ = collector.emitHealth(finalContext, sink, scanErr)
	}
	return errors.Join(err, flushErr, scanErr)
}

func (collector *DirectoryCollector) emitRemovableVolumeUnavailable(ctx context.Context, sink Sink, cause error) error {
	payload, _ := json.Marshal(struct {
		Root  string `json:"root"`
		Error string `json:"error"`
	}{Root: collector.root, Error: cause.Error()})
	return sink.Emit(ctx, Observation{
		Category:    domain.EventCategoryDevice,
		Action:      "removable_volume_unavailable",
		Severity:    domain.EventSeverityHigh,
		ObservedUTC: collector.now().UTC(),
		ObjectKey:   collector.root,
		Source:      collector.Name(),
		Confidence:  domain.EventConfidenceDirect,
		Payload:     payload,
	})
}

func (collector *DirectoryCollector) emitChanges(ctx context.Context, sink Sink, changes []FileChange) error {
	observedUTC := collector.now().UTC()
	if collector.pendingRename != nil && observedUTC.Sub(collector.pendingRenameUTC) > time.Second {
		if err := collector.flushRename(ctx, sink); err != nil {
			return err
		}
	}
	for _, change := range changes {
		if collector.pendingRename != nil {
			if change.Action == fileActionRenamedNewName && collector.matchesTarget(change.RelativePath) {
				old := collector.pendingRename.RelativePath
				collector.pendingRename = nil
				if err := collector.emit(ctx, sink, "file_renamed", change.RelativePath, old, observedUTC); err != nil {
					return err
				}
				continue
			}
			if err := collector.flushRename(ctx, sink); err != nil {
				return err
			}
		}
		if !collector.matchesTarget(change.RelativePath) {
			continue
		}
		if change.Action == fileActionRenamedOldName {
			if _, err := collector.resolve(change.RelativePath); err != nil {
				return err
			}
			pending := change
			collector.pendingRename, collector.pendingRenameUTC = &pending, observedUTC
			continue
		}

		action := fileActionName(change.Action)
		if action == "" {
			continue
		}
		if action == "file_modified" && collector.wasTruncated(change.RelativePath) {
			action = "file_truncated"
		}
		if err := collector.emit(ctx, sink, action, change.RelativePath, "", observedUTC); err != nil {
			return err
		}
	}
	return nil
}

func (collector *DirectoryCollector) matchesTarget(relativePath string) bool {
	return collector.targetFile == "" || strings.EqualFold(filepath.Clean(relativePath), collector.targetFile)
}

func (collector *DirectoryCollector) flushRename(ctx context.Context, sink Sink) error {
	if collector.pendingRename == nil {
		return nil
	}
	pending, observed := collector.pendingRename, collector.pendingRenameUTC
	collector.pendingRename = nil
	return collector.emit(ctx, sink, "file_rename_old_name", pending.RelativePath, "", observed)
}

func (collector *DirectoryCollector) emit(
	ctx context.Context,
	sink Sink,
	action string,
	relativePath string,
	oldRelativePath string,
	observedUTC time.Time,
) error {
	return collector.emitRecord(ctx, sink, action, relativePath, oldRelativePath, observedUTC, "")
}

func (collector *DirectoryCollector) emitRecord(ctx context.Context, sink Sink, action, relativePath, oldRelativePath string, observedUTC time.Time, reconciliation string) error {
	path, err := collector.resolve(relativePath)
	if err != nil {
		return err
	}
	if !collector.allowsAction(action) {
		if reconciliation == "" {
			collector.rememberNotification(action, relativePath, oldRelativePath)
		}
		return nil
	}
	payload := FileChangePayload{Root: collector.root, Volume: filepath.VolumeName(path), Path: path, Reconciliation: reconciliation}
	if action == "file_modified" || action == "file_truncated" {
		if before, ok := collector.knownFingerprint(relativePath); ok {
			previousSize := before.size
			payload.PreviousSize = &previousSize
		}
		if info, statErr := os.Lstat(path); statErr == nil {
			currentSize := info.Size()
			payload.CurrentSize = &currentSize
		}
	}
	if domain.MatchesMonitoringExclusion(collector.exclusions, path, "") {
		return nil
	}
	queueLargeHash := false
	if collector.policy.CaptureContentHash && action != "file_deleted" && action != "file_rename_old_name" {
		payload.ContentSHA256, payload.ContentHashStatus = hashFileContent(path)
		queueLargeHash = payload.ContentHashStatus == "skipped_size_limit"
		if queueLargeHash {
			payload.ContentHashStatus = "waiting"
		}
	}
	if oldRelativePath != "" {
		payload.OldPath, err = collector.resolve(oldRelativePath)
		if err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode file observation: %w", err)
	}
	observation := Observation{
		Category:    domain.EventCategoryFile,
		Action:      action,
		Severity:    domain.EventSeverityLow,
		ObservedUTC: observedUTC,
		ObjectKey:   path,
		Source:      collector.Name(),
		Confidence:  domain.EventConfidenceDirect,
		Payload:     encoded,
	}
	if reconciliation != "" {
		observation.Confidence = domain.EventConfidenceSnapshotDiff
	}
	if err := sink.Emit(ctx, observation); err != nil && !errors.Is(err, ErrObservationQueueFull) {
		return fmt.Errorf("emit %s observation: %w", action, err)
	}
	if queueLargeHash && !collector.enqueueLargeFileHash(ctx, sink, path, observedUTC) {
		_ = collector.emitHashQueueOverflow(ctx, sink, path, observedUTC)
	}
	if reconciliation == "" {
		collector.rememberNotification(action, relativePath, oldRelativePath)
	}
	return nil
}

func (collector *DirectoryCollector) allowsAction(action string) bool {
	switch action {
	case "file_created", "file_added":
		return collector.policy.RecordCreate
	case "file_modified", "file_truncated":
		return collector.policy.RecordModify
	case "file_deleted", "file_removed":
		return collector.policy.RecordDelete
	case "file_renamed", "file_rename_old_name", "file_rename_new_name":
		return collector.policy.RecordRename
	default:
		return true
	}
}

func (collector *DirectoryCollector) wasTruncated(relativePath string) bool {
	before, ok := collector.knownFingerprint(relativePath)
	if !ok {
		return false
	}
	info, err := os.Lstat(filepath.Join(collector.root, filepath.Clean(relativePath)))
	return err == nil && info.Mode().IsRegular() && info.Size() < before.size
}

func (collector *DirectoryCollector) knownFingerprint(relativePath string) (fileFingerprint, bool) {
	cleaned := filepath.Clean(relativePath)
	if known, ok := collector.knownFiles[cleaned]; ok {
		return known, true
	}
	for path, known := range collector.knownFiles {
		if strings.EqualFold(path, cleaned) {
			return known, true
		}
	}
	return fileFingerprint{}, false
}

func (collector *DirectoryCollector) enqueueLargeFileHash(ctx context.Context, sink Sink, path string, observedUTC time.Time) bool {
	if collector.hashQueue == nil {
		queue, err := NewFileHashQueue(FileHashQueueOptions{Capacity: 32, BytesPerSecond: defaultLargeFileHashBytesPerSecond})
		if err != nil {
			return false
		}
		collector.hashQueue = queue
	}
	return collector.hashQueue.Enqueue(ctx, path, func(contentSHA256, status string) {
		completionContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		payload, err := json.Marshal(FileChangePayload{
			Root: collector.root, Volume: filepath.VolumeName(path), Path: path, ContentSHA256: contentSHA256, ContentHashStatus: status,
		})
		if err != nil {
			return
		}
		_ = sink.Emit(completionContext, Observation{
			Category: domain.EventCategoryFile, Action: "file_hash_completed", Severity: domain.EventSeverityLow,
			ObservedUTC: collector.now().UTC(), ObjectKey: path, Source: collector.Name(),
			Confidence: domain.EventConfidenceDirect, Payload: payload,
		})
	})
}

func (collector *DirectoryCollector) emitHashQueueOverflow(ctx context.Context, sink Sink, path string, observedUTC time.Time) error {
	payload, _ := json.Marshal(struct {
		Path string `json:"path"`
	}{Path: path})
	return sink.Emit(ctx, Observation{
		Category: domain.EventCategoryHealth, Action: "file_hash_queue_overflow", Severity: domain.EventSeverityMedium,
		ObservedUTC: observedUTC, ObjectKey: path, Source: collector.Name(), Confidence: domain.EventConfidenceDirect,
		Payload: payload,
	})
}

func (collector *DirectoryCollector) closeHashQueue() {
	if collector.hashQueue != nil {
		collector.hashQueue.Close()
		collector.hashQueue = nil
	}
}

func (collector *DirectoryCollector) emitHealth(ctx context.Context, sink Sink, cause error) error {
	healthContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	payload, _ := json.Marshal(struct {
		Root  string `json:"root"`
		Error string `json:"error"`
	}{Root: collector.root, Error: cause.Error()})
	return sink.Emit(healthContext, Observation{
		Category:    domain.EventCategoryHealth,
		Action:      "directory_snapshot_required",
		Severity:    domain.EventSeverityHigh,
		ObservedUTC: collector.now().UTC(),
		ObjectKey:   collector.root,
		Source:      collector.Name(),
		Confidence:  domain.EventConfidenceDirect,
		Payload:     payload,
	})
}

func (collector *DirectoryCollector) resolve(relativePath string) (string, error) {
	cleaned := filepath.Clean(relativePath)
	if cleaned == "." || filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("directory change path escapes watched root")
	}
	return filepath.Join(collector.root, cleaned), nil
}

func fileActionName(action uint32) string {
	switch action {
	case fileActionAdded:
		return "file_created"
	case fileActionRemoved:
		return "file_deleted"
	case fileActionModified:
		return "file_modified"
	case fileActionRenamedOldName:
		return "file_rename_old_name"
	case fileActionRenamedNewName:
		return "file_rename_new_name"
	default:
		return ""
	}
}
