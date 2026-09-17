package collector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestDirectoryOverflowActuallyReconcilesPersistedChanges(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"modified.txt", "deleted.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(context.Context, string, bool, func([]FileChange) error) error {
		if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("new"), 0600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "modified.txt"), []byte("after modification"), 0600); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
			return err
		}
		return ErrDirectoryChangeOverflow
	}
	collector, _ := newDirectoryCollector(root, true, watch, time.Now)
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); !errors.Is(err, ErrDirectoryChangeOverflow) {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, event := range sink.snapshot() {
		if event.Category == domain.EventCategoryFile {
			if event.Confidence != domain.EventConfidenceSnapshotDiff {
				t.Fatalf("rescan was presented as a direct notification: %+v", event)
			}
			found[event.Action] = true
		}
	}
	for _, action := range []string{"file_created", "file_modified", "file_deleted"} {
		if !found[action] {
			t.Errorf("overflow missed %s", action)
		}
	}
}

func TestDirectoryFinalScanRunsWithLiveDrainContext(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch := func(context.Context, string, bool, func([]FileChange) error) error {
		if err := os.WriteFile(filepath.Join(root, "missed-at-stop.txt"), []byte("change"), 0600); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	}
	collector, _ := newDirectoryCollector(root, true, watch, time.Now)
	sink := &liveContextObservationSink{}
	if err := collector.Run(ctx, sink); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	found := false
	for _, event := range sink.snapshot() {
		if event.Action == "file_created" && filepath.Base(event.ObjectKey) == "missed-at-stop.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("final scan lost an unnotified file change")
	}
}

func TestDirectoryReconcilesGapAfterWatcherArms(t *testing.T) {
	root := t.TempDir()
	sink := &observationSink{}
	watch := func(_ context.Context, _ string, _ bool, handle func([]FileChange) error) error {
		if err := os.WriteFile(filepath.Join(root, "between-baseline-and-watch.txt"), []byte("change"), 0600); err != nil {
			return err
		}
		if err := handle(nil); err != nil {
			return err
		}
		if len(sink.snapshot()) == 0 {
			t.Fatal("watch registration gap was not reconciled")
		}
		return nil
	}
	collector, _ := newDirectoryCollector(root, true, watch, time.Now)
	if err := collector.Run(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(sink.snapshot()[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["reconciliation"] != "watch_start" {
		t.Fatalf("rescan reason missing: %v", payload)
	}
}

type liveContextObservationSink struct{ observationSink }

func TestSnapshotFailureStillRecordsGapAfterScanContextExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	collector, _ := newDirectoryCollector(t.TempDir(), true, func(context.Context, string, bool, func([]FileChange) error) error { return nil }, time.Now)
	sink := &liveContextObservationSink{}
	if err := collector.emitHealth(ctx, sink, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if len(sink.snapshot()) != 1 {
		t.Fatal("expired final scan hid its coverage gap")
	}
}

func (sink *liveContextObservationSink) Emit(ctx context.Context, event Observation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return sink.observationSink.Emit(ctx, event)
}
