package collector

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"desktopguardpro/internal/domain"
)

func TestParseFileNotifyInformation(t *testing.T) {
	t.Parallel()

	buffer := encodeFileNotifications(
		FileChange{Action: fileActionRenamedOldName, RelativePath: `old.txt`},
		FileChange{Action: fileActionRenamedNewName, RelativePath: `new.txt`},
	)
	changes, err := parseFileNotifyInformation(buffer)
	if err != nil {
		t.Fatalf("parseFileNotifyInformation() error = %v", err)
	}
	if len(changes) != 2 || changes[0].RelativePath != "old.txt" || changes[1].RelativePath != "new.txt" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestParseFileNotifyInformationRejectsTruncatedRecord(t *testing.T) {
	t.Parallel()

	buffer := encodeFileNotifications(FileChange{Action: fileActionAdded, RelativePath: `file.txt`})
	buffer = buffer[:len(buffer)-1]
	if _, err := parseFileNotifyInformation(buffer); err == nil {
		t.Fatal("parseFileNotifyInformation() error = nil, want truncation error")
	}
}

func TestDirectoryCollectorPairsRenameAndProtectsRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "watched")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	watch := func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		return handle([]FileChange{
			{Action: fileActionRenamedOldName, RelativePath: `old.txt`},
			{Action: fileActionRenamedNewName, RelativePath: `nested\new.txt`},
		})
	}
	collector, err := newDirectoryCollector(root, true, watch, func() time.Time {
		return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("newDirectoryCollector() error = %v", err)
	}
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "file_renamed" {
		t.Fatalf("observations = %+v", observations)
	}
	var payload FileChangePayload
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.OldPath != filepath.Join(root, "old.txt") || payload.Path != filepath.Join(root, `nested\new.txt`) ||
		payload.Volume != filepath.VolumeName(payload.Path) {
		t.Fatalf("rename payload = %+v", payload)
	}
	if _, err := collector.resolve(`..\escape.txt`); err == nil {
		t.Fatal("resolve() allowed path outside watched root")
	}
}

func TestDirectoryCollectorRecordsContentHashForExistingRegularFile(t *testing.T) {
	root := t.TempDir()
	content := []byte("Desktop Guard Pro audit content")
	if err := os.WriteFile(filepath.Join(root, "document.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := newDirectoryCollector(root, true, func(context.Context, string, bool, func([]FileChange) error) error {
		return nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	sink := &observationSink{}
	if err := collector.emitRecord(context.Background(), sink, "file_modified", "document.txt", "", time.Now().UTC(), ""); err != nil {
		t.Fatalf("emitRecord() error = %v", err)
	}
	var payload FileChangePayload
	if err := json.Unmarshal(sink.snapshot()[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if payload.ContentSHA256 != hex.EncodeToString(digest[:]) || payload.ContentHashStatus != "available" {
		t.Fatalf("file payload hash = %+v, want SHA-256 for written content", payload)
	}
}

func TestDirectoryCollectorPolicyFiltersActionsAndContentHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "document.txt")
	if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := newDirectoryCollector(root, true, func(context.Context, string, bool, func([]FileChange) error) error {
		return nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	collector.SetAuditPolicy(domain.FileAuditPolicy{RecordCreate: true})
	sink := &observationSink{}
	if err := collector.emitRecord(context.Background(), sink, "file_modified", "document.txt", "", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	if err := collector.emitRecord(context.Background(), sink, "file_created", "document.txt", "", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "file_created" {
		t.Fatalf("filtered observations = %#v", observations)
	}
	var payload FileChangePayload
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ContentHashStatus != "" || payload.ContentSHA256 != "" {
		t.Fatalf("hash fields were retained while hashing disabled: %#v", payload)
	}
}

func TestDirectoryCollectorClassifiesSizeReductionAsTruncation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "evidence.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := newDirectoryCollector(root, true, func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		if err := os.WriteFile(path, []byte("012"), 0o600); err != nil {
			return err
		}
		return handle([]FileChange{{Action: fileActionModified, RelativePath: "evidence.log"}})
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "file_truncated" {
		t.Fatalf("truncation observations = %+v", observations)
	}
	var payload FileChangePayload
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PreviousSize == nil || *payload.PreviousSize != 10 || payload.CurrentSize == nil || *payload.CurrentSize != 3 {
		t.Fatalf("truncation payload = %+v", payload)
	}
}

func TestDirectoryCollectorQueuesLargeFileHashAndEmitsCompletion(t *testing.T) {
	previousLimit := immediateFileHashByteLimit
	immediateFileHashByteLimit = 1
	t.Cleanup(func() { immediateFileHashByteLimit = previousLimit })
	root := t.TempDir()
	content := []byte("queued content")
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := newDirectoryCollector(root, true, func(context.Context, string, bool, func([]FileChange) error) error {
		return nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(collector.closeHashQueue)
	sink := &observationSink{}
	if err := collector.emitRecord(context.Background(), sink, "file_modified", "large.bin", "", time.Now().UTC(), ""); err != nil {
		t.Fatalf("emitRecord() error = %v", err)
	}
	deadline := time.After(time.Second)
	for {
		observations := sink.snapshot()
		if len(observations) >= 2 {
			var initial, completed FileChangePayload
			if err := json.Unmarshal(observations[0].Payload, &initial); err != nil {
				t.Fatalf("decode initial payload: %v", err)
			}
			if err := json.Unmarshal(observations[1].Payload, &completed); err != nil {
				t.Fatalf("decode completion payload: %v", err)
			}
			digest := sha256.Sum256(content)
			if initial.ContentHashStatus != "waiting" || observations[1].Action != "file_hash_completed" ||
				completed.ContentHashStatus != "available" || completed.ContentSHA256 != hex.EncodeToString(digest[:]) ||
				completed.Volume != filepath.VolumeName(path) {
				t.Fatalf("queued hash observations = %+v, initial=%+v completed=%+v", observations, initial, completed)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("large file hash completion was not emitted: %+v", observations)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestFileCollectorFiltersSiblingDirectoryChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := newDirectoryCollector(root, false, func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		return handle([]FileChange{
			{Action: fileActionModified, RelativePath: "other.txt"},
			{Action: fileActionModified, RelativePath: "target.txt"},
		})
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	collector.targetFile = "target.txt"
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].ObjectKey != filepath.Join(root, "target.txt") {
		t.Fatalf("file target observations = %+v", observations)
	}
}

func TestDirectoryCollectorAppliesMonitoringExclusions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ignored.tmp"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kept.txt"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	collector, err := newDirectoryCollector(root, false, func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		return handle([]FileChange{
			{Action: fileActionModified, RelativePath: "ignored.tmp"},
			{Action: fileActionModified, RelativePath: "kept.txt"},
		})
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	collector.exclusions = []domain.MonitoringExclusion{{
		Kind: domain.MonitoringExclusionKindExtension, Pattern: ".tmp",
	}}
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].ObjectKey != filepath.Join(root, "kept.txt") {
		t.Fatalf("excluded observations = %+v", observations)
	}
}

func TestDirectoryCollectorPairsRenameAcrossNotificationBatches(t *testing.T) {
	watch := func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		if err := handle([]FileChange{{Action: fileActionRenamedOldName, RelativePath: "old.txt"}}); err != nil {
			return err
		}
		return handle([]FileChange{{Action: fileActionRenamedNewName, RelativePath: "new.txt"}})
	}
	collector, _ := newDirectoryCollector(t.TempDir(), true, watch, time.Now)
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	observed := sink.snapshot()
	if len(observed) != 1 || observed[0].Action != "file_renamed" {
		t.Fatalf("cross-batch rename=%+v", observed)
	}
}

func TestDirectoryCollectorDoesNotPairRenameAcrossOtherNotifications(t *testing.T) {
	watch := func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		if err := handle([]FileChange{{Action: fileActionRenamedOldName, RelativePath: "old.txt"}}); err != nil {
			return err
		}
		return handle([]FileChange{{Action: fileActionModified, RelativePath: "other.txt"}, {Action: fileActionRenamedNewName, RelativePath: "new.txt"}})
	}
	collector, _ := newDirectoryCollector(t.TempDir(), true, watch, time.Now)
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	for _, event := range sink.snapshot() {
		if event.Action == "file_renamed" {
			t.Fatal("unrelated rename halves were joined")
		}
	}
}

func TestDirectoryCollectorEmitsHealthOnOverflow(t *testing.T) {
	t.Parallel()

	watch := func(context.Context, string, bool, func([]FileChange) error) error {
		return ErrDirectoryChangeOverflow
	}
	collector, err := newDirectoryCollector(t.TempDir(), true, watch, time.Now)
	if err != nil {
		t.Fatalf("newDirectoryCollector() error = %v", err)
	}
	sink := &observationSink{}
	err = collector.Run(context.Background(), sink)
	if !errors.Is(err, ErrDirectoryChangeOverflow) {
		t.Fatalf("Run() error = %v, want %v", err, ErrDirectoryChangeOverflow)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "directory_snapshot_required" {
		t.Fatalf("health observations = %+v", observations)
	}
}

func TestRemovableVolumeCollectorEmitsUnavailableEventWhenWatchStops(t *testing.T) {
	watchFailure := errors.New("volume is no longer available")
	collector, err := newDirectoryCollector(t.TempDir(), true, func(context.Context, string, bool, func([]FileChange) error) error {
		return watchFailure
	}, time.Now)
	if err != nil {
		t.Fatalf("newDirectoryCollector() error = %v", err)
	}
	collector.removableVolume = true
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); !errors.Is(err, watchFailure) {
		t.Fatalf("Run() error = %v, want %v", err, watchFailure)
	}
	for _, observation := range sink.snapshot() {
		if observation.Category == domain.EventCategoryDevice && observation.Action == "removable_volume_unavailable" {
			return
		}
	}
	t.Fatalf("observations = %+v", sink.snapshot())
}

type channelObservationSink chan Observation

func (sink channelObservationSink) Emit(_ context.Context, observation Observation) error {
	sink <- observation
	return nil
}

func TestWindowsDirectoryCollectorObservesCreatedFileAndCancels(t *testing.T) {
	root := t.TempDir()
	collector, err := NewDirectoryCollector(root, true)
	if err != nil {
		t.Fatalf("NewDirectoryCollector() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sink := make(channelObservationSink, 8)
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx, sink) }()

	time.Sleep(100 * time.Millisecond)
	target := filepath.Join(root, "created.txt")
	if err := os.WriteFile(target, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	found := false
	for !found {
		select {
		case observation := <-sink:
			found = observation.Action == "file_created" && observation.ObjectKey == target
		case <-ctx.Done():
			t.Fatal("directory collector did not observe created file")
		}
	}
	cancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Run() error = %v, want %v", runErr, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("directory collector did not stop after cancellation")
	}
}

func TestNativeDirectoryWatchSignalsArmedBeforeFirstChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	armed := false
	err := watchWindowsDirectory(ctx, t.TempDir(), true, func(changes []FileChange) error {
		if len(changes) == 0 {
			armed = true
			cancel()
		}
		return nil
	})
	if !armed || !errors.Is(err, context.Canceled) {
		t.Fatalf("watch armed=%v, error=%v", armed, err)
	}
}

func TestDirectoryRenameOldNameFlushesOnIdleHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	sink := &observationSink{}
	watch := func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		if err := handle(nil); err != nil {
			return err
		}
		if err := handle([]FileChange{{Action: fileActionRenamedOldName, RelativePath: "unpaired.txt"}}); err != nil {
			return err
		}
		now = now.Add(2 * time.Second)
		if err := handle(nil); err != nil {
			return err
		}
		if len(sink.snapshot()) != 1 {
			t.Fatal("orphan rename remains buffered while idle")
		}
		return nil
	}
	collector, _ := newDirectoryCollector(t.TempDir(), true, watch, func() time.Time { return now })
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
}

func encodeFileNotifications(changes ...FileChange) []byte {
	var buffer []byte
	for index, change := range changes {
		name := utf16.Encode([]rune(change.RelativePath))
		recordLength := fileNotifyHeaderSize + len(name)*2
		paddedLength := (recordLength + 3) &^ 3
		record := make([]byte, paddedLength)
		if index < len(changes)-1 {
			binary.LittleEndian.PutUint32(record[0:4], uint32(paddedLength))
		}
		binary.LittleEndian.PutUint32(record[4:8], change.Action)
		binary.LittleEndian.PutUint32(record[8:12], uint32(len(name)*2))
		for nameIndex, character := range name {
			binary.LittleEndian.PutUint16(record[12+nameIndex*2:], character)
		}
		buffer = append(buffer, record...)
	}
	return buffer
}
