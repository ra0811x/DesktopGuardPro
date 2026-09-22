package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/collector"
	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/installpolicy"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
)

const runtimeTestOwnerSID = "S-1-5-21-1000-1001-1002-1003"

func TestCaptureStartBaselinesStoresPartialFailureReview(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openTestApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-review", Name: "基线失败汇总",
	})
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionTransition, coreservice.TransitionSessionRequest{
		State: domain.SessionStatePreparing,
	})

	called := make([]string, 0, 3)
	capture := captureStartBaselinesWithReview(runtime.coordinator, runtime.repository,
		namedBaselineCapture{Name: "asset_inventory", Capture: func(context.Context, string) error {
			called = append(called, "asset_inventory")
			return nil
		}},
		namedBaselineCapture{Name: "usn_checkpoint", Capture: func(context.Context, string) error {
			called = append(called, "usn_checkpoint")
			return errors.New("journal unavailable")
		}},
		namedBaselineCapture{Name: "priority_files", Capture: func(context.Context, string) error {
			called = append(called, "priority_files")
			return errors.New("path access denied")
		}},
	)
	if err := capture(context.Background(), "session-review"); err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	if strings.Join(called, ",") != "asset_inventory,usn_checkpoint,priority_files" {
		t.Fatalf("capture order = %v", called)
	}

	current, ok := runtime.coordinator.Current()
	if !ok || current.State != domain.SessionStateBaselineReview {
		t.Fatalf("current session = %+v, exists = %t", current, ok)
	}
	persisted, err := runtime.repository.GetSession(context.Background(), "session-review")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if persisted.State != domain.SessionStateBaselineReview {
		t.Fatalf("persisted state = %q", persisted.State)
	}
	review, err := runtime.repository.LoadBaselineReview(context.Background(), "session-review")
	if err != nil {
		t.Fatalf("LoadBaselineReview() error = %v", err)
	}
	if review.Decision.Status != domain.BaselineCaptureStatusPartialFailure ||
		!review.Decision.CanContinue || !review.Decision.CanCancel || len(review.Decision.Failures) != 2 {
		t.Fatalf("baseline review = %+v", review)
	}
	records, err := runtime.repository.ListEvents(context.Background(), "session-review")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if _, found := findRuntimeEvent(records, "baseline_review_required"); !found {
		t.Fatalf("baseline review event missing: %+v", records)
	}
}

func TestApplicationRuntimeRestoresPersistedSession(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() first error = %v", err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-1", Name: "恢复测试",
	})
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionTransition, coreservice.TransitionSessionRequest{
		State: domain.SessionStatePreparing,
	})
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}

	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() second error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	current, ok := runtime.coordinator.Current()
	if !ok {
		t.Fatal("Current() ok = false after restart")
	}
	if current.ID != "session-1" || current.State != domain.SessionStatePreparing || current.Revision != 1 {
		t.Fatalf("Current() after restart = %+v", current)
	}
}

func TestApplicationRuntimeRecoversActiveSessionAsDegradedWithRecoveryEvent(t *testing.T) {
	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() first error = %v", err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-1", Name: "恢复降级测试",
	})
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionTransition, coreservice.TransitionSessionRequest{
		State: domain.SessionStatePreparing,
	})
	if _, err := runtime.coordinator.TransitionPersisted(domain.SessionStateActive, func(session domain.Session) error {
		return runtime.repository.UpdateSession(context.Background(), session, time.Now().UTC())
	}); err != nil {
		t.Fatalf("TransitionPersisted(active) error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}

	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() second error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	current, ok := runtime.coordinator.Current()
	if !ok || current.State != domain.SessionStateDegraded {
		t.Fatalf("recovered session = %+v, exists=%t", current, ok)
	}
	records, err := runtime.repository.ListEvents(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	recoveryRecord, found := findRuntimeEvent(records, "service_recovered_after_interruption")
	if !found || recoveryRecord.Event.Category != domain.EventCategoryHealth || recoveryRecord.Event.Sequence != uint64(len(records)) {
		t.Fatalf("recovery records = %+v", records)
	}
	var payload struct {
		InterruptionStartUTC time.Time `json:"interruptionStartUtc"`
		RecoveredUTC         time.Time `json:"recoveredUtc"`
		DurationMS           int64     `json:"durationMillis"`
		PreviousSequence     uint64    `json:"previousSequence"`
		BoundaryBasis        string    `json:"boundaryBasis"`
	}
	if err := json.Unmarshal(recoveryRecord.Payload, &payload); err != nil {
		t.Fatalf("decode recovery payload: %v", err)
	}
	if payload.RecoveredUTC.IsZero() || payload.InterruptionStartUTC.IsZero() || payload.DurationMS < 0 ||
		payload.BoundaryBasis == "" || payload.PreviousSequence != recoveryRecord.Event.Sequence-1 {
		t.Fatalf("recovery payload = %+v", payload)
	}
}

func TestApplicationRuntimeCompletesFinalizingSessionInterruptedByRestart(t *testing.T) {
	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() first error = %v", err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-finalizing", Name: "收尾中断恢复测试",
	})
	for _, state := range []domain.SessionState{
		domain.SessionStatePreparing,
		domain.SessionStateActive,
		domain.SessionStateFinalizing,
	} {
		if _, err := runtime.coordinator.TransitionPersisted(state, func(session domain.Session) error {
			return runtime.repository.UpdateSession(context.Background(), session, time.Now().UTC())
		}); err != nil {
			t.Fatalf("TransitionPersisted(%s) error = %v", state, err)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}

	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() second error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	current, ok := runtime.coordinator.Current()
	if !ok || current.State != domain.SessionStateCompleted {
		t.Fatalf("recovered session = %+v, exists=%t", current, ok)
	}
	records, err := runtime.repository.ListEvents(context.Background(), "session-finalizing")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	recoveryRecord, found := findRuntimeEvent(records, "protection_ended")
	if !found || recoveryRecord.Event.Source != "desktop_guard_service" {
		t.Fatalf("finalization recovery records = %+v", records)
	}
}

func TestApplicationRuntimeRecordsRecoveryBoundaryForDegradedSession(t *testing.T) {
	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() first error = %v", err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-1", Name: "降级恢复测试",
	})
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStateDegraded} {
		if _, err := runtime.coordinator.TransitionPersisted(state, func(session domain.Session) error {
			return runtime.repository.UpdateSession(context.Background(), session, time.Now().UTC())
		}); err != nil {
			t.Fatalf("TransitionPersisted(%s) error = %v", state, err)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}

	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() second error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	current, ok := runtime.coordinator.Current()
	if !ok || current.State != domain.SessionStateDegraded {
		t.Fatalf("recovered session = %+v, exists=%t", current, ok)
	}
	records, err := runtime.repository.ListEvents(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if _, found := findRuntimeEvent(records, "service_recovered_after_interruption"); !found {
		t.Fatalf("recovery records = %+v", records)
	}
}

func TestApplicationRuntimeCreatesCollectorController(t *testing.T) {
	t.Parallel()

	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.collectors == nil {
		t.Fatal("collector controller was not created")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.collectors.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("collector controller shutdown error = %v", err)
	}
}

func TestApplicationRuntimeBuildsCollectorsFromMonitoringTargets(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.repository.SaveMonitoringTargets(context.Background(), []domain.MonitoringTarget{
		{Path: filepath.Join(t.TempDir(), "summary.txt"), Kind: domain.MonitoringTargetKindFile},
	}); err != nil {
		t.Fatalf("SaveMonitoringTargets() error = %v", err)
	}
	if _, err := collectorsForRepository(runtime.repository); err != nil {
		t.Fatalf("collectorsForRepository() error = %v", err)
	}
}

func TestApplicationRuntimeBuildsStrictReadAuditCollectorForStrictSession(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.repository.SaveMonitoringTargets(context.Background(), []domain.MonitoringTarget{{
		Path: t.TempDir(), Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}); err != nil {
		t.Fatalf("SaveMonitoringTargets() error = %v", err)
	}
	collectors, err := collectorsForRepositoryWithMonitoringLevel(runtime.repository, domain.MonitoringLevelStrict)
	if err != nil {
		t.Fatalf("collectorsForRepositoryWithMonitoringLevel() error = %v", err)
	}
	for _, current := range collectors {
		if _, ok := current.(*collector.StrictReadAuditCollector); ok {
			return
		}
	}
	t.Fatal("strict session collector set does not include object access audit")
}

func TestApplicationRuntimeBuildsCollectorsFromSessionPolicy(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := runtime.repository.SaveMonitoringTargets(context.Background(), []domain.MonitoringTarget{{
		Path: t.TempDir(), Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}); err != nil {
		t.Fatal(err)
	}
	policy := domain.MonitoringPolicy{FileActivityEnabled: true}
	collectors, err := collectorsForRepositoryWithMonitoringPolicy(runtime.repository, policy)
	if err != nil {
		t.Fatalf("collectorsForRepositoryWithMonitoringPolicy() error = %v", err)
	}
	if len(collectors) != 1 {
		t.Fatalf("collector count = %d, want 1", len(collectors))
	}
	if _, ok := collectors[0].(*collector.DirectoryCollector); !ok {
		t.Fatalf("collector type = %T, want *collector.DirectoryCollector", collectors[0])
	}
}

func TestAssetBaselineFilteringRespectsMonitoringPolicy(t *testing.T) {
	baseline := domain.AssetBaseline{Assets: []domain.Asset{
		{Category: domain.AssetCategorySoftware, Identifier: "software", DisplayName: "软件"},
		{Category: domain.AssetCategoryDevice, Identifier: "device", DisplayName: "设备"},
		{Category: domain.AssetCategoryNetwork, Identifier: "network", DisplayName: "网络"},
		{Category: domain.AssetCategoryAccount, Identifier: "account", DisplayName: "账户"},
		{Category: domain.AssetCategorySystem, Identifier: "system", DisplayName: "系统"},
	}}
	policy := domain.MonitoringPolicy{ExternalDevicesEnabled: true}
	filtered := filterAssetBaselineForMonitoringPolicy(baseline, policy)
	if len(filtered.Assets) != 1 || filtered.Assets[0].Category != domain.AssetCategoryDevice {
		t.Fatalf("filtered assets = %#v", filtered.Assets)
	}
}

func TestUSNRecoveryGapsDetectJournalReplacementAndAdvancedPosition(t *testing.T) {
	start := []storage.USNJournalCheckpoint{
		{Volume: `C:\`, JournalID: 1, NextUSN: 100},
		{Volume: `D:\`, JournalID: 2, NextUSN: 200},
	}
	current := []collector.USNJournalState{
		{Volume: `C:\`, JournalID: 1, NextUSN: 101},
		{Volume: `D:\`, JournalID: 3, NextUSN: 201},
	}
	gaps := usnRecoveryGaps(start, current)
	if len(gaps) != 2 || gaps[0].Volume != `C:\` || gaps[1].Volume != `D:\` {
		t.Fatalf("USN recovery gaps = %#v", gaps)
	}
}

func TestUSNChangeActionMapsCreateDeleteRenameAndContentChanges(t *testing.T) {
	cases := []struct {
		reason uint32
		want   string
	}{
		{collector.USNReasonFileCreate, "file_created"},
		{collector.USNReasonFileDelete, "file_deleted"},
		{collector.USNReasonRenameNewName, "file_renamed"},
		{collector.USNReasonDataTruncation, "file_truncated"},
		{collector.USNReasonDataOverwrite, "file_modified"},
	}
	for _, test := range cases {
		if got := usnChangeAction(test.reason); got != test.want {
			t.Errorf("usnChangeAction(%#x) = %q, want %q", test.reason, got, test.want)
		}
	}
}

func TestUSNRecoveryPathMatchesConfiguredTargetScope(t *testing.T) {
	targets := []domain.MonitoringTarget{
		{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: false},
		{Path: `C:\Archive`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
		{Path: `D:\focus.txt`, Kind: domain.MonitoringTargetKindFile},
	}
	cases := map[string]bool{
		`C:\Evidence\direct.txt`:        true,
		`C:\Evidence\nested\hidden.txt`: false,
		`C:\Archive\nested\kept.txt`:    true,
		`D:\focus.txt`:                  true,
		`D:\other.txt`:                  false,
	}
	for path, want := range cases {
		if got := usnRecoveryPathMatches(targets, path); got != want {
			t.Errorf("usnRecoveryPathMatches(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestPowerResumeRecordsSleepInterruptionBoundary(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	session, err := domain.NewSession("session-sleep", "睡眠恢复测试")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.repository.CreateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := session.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatal(err)
	}
	if err := runtime.repository.UpdateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := session.Transition(domain.SessionStateActive); err != nil {
		t.Fatal(err)
	}
	if err := runtime.repository.UpdateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.coordinator.Restore(*session); err != nil {
		t.Fatal(err)
	}
	if err := runtime.handlePowerEvent(powerEventSuspend); err != nil {
		t.Fatal(err)
	}
	if err := runtime.handlePowerEvent(powerEventResumeAuto); err != nil {
		t.Fatal(err)
	}
	records, err := runtime.repository.ListEvents(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Event.Action != "system_sleep_started" || records[1].Event.Action != "service_recovered_after_sleep" {
		t.Fatalf("power boundary records = %#v", records)
	}
	if !strings.Contains(string(records[1].Payload), "durationMillis") {
		t.Fatalf("resume payload = %s", records[1].Payload)
	}
}

func TestStorageFileBaselineEntriesPreservesAllFileTimestamps(t *testing.T) {
	created := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
	accessed := created.Add(time.Hour)
	modified := accessed.Add(time.Hour)
	converted := storageFileBaselineEntries([]collector.FileBaselineEntry{{
		Path: `C:\Evidence\report.txt`, Size: 12, Mode: 0o600,
		CreatedUTC: created.UnixNano(), AccessedUTC: accessed.UnixNano(), ModifiedUTC: modified.UnixNano(),
		ContentSHA256: "abc", HashStatus: "available",
	}})
	if len(converted) != 1 || !converted[0].CreatedUTC.Equal(created) ||
		!converted[0].AccessedUTC.Equal(accessed) || !converted[0].ModifiedUTC.Equal(modified) {
		t.Fatalf("converted baseline = %#v", converted)
	}
}

func TestFileBaselineDifferencesClassifiesAddedRemovedAndChangedFiles(t *testing.T) {
	modifiedUTC := time.Date(2026, time.September, 10, 4, 0, 0, 0, time.UTC)
	before := []storage.FileBaselineEntry{
		{Path: `C:\\guard\\changed.txt`, Size: 10, Mode: 0o644, ModifiedUTC: modifiedUTC, ContentSHA256: "old", HashStatus: "available"},
		{Path: `C:\\guard\\removed.txt`, Size: 10, Mode: 0o644, ModifiedUTC: modifiedUTC, ContentSHA256: "removed", HashStatus: "available"},
		{Path: `C:\\guard\\unchanged.txt`, Size: 10, Mode: 0o644, ModifiedUTC: modifiedUTC, ContentSHA256: "same", HashStatus: "available"},
	}
	after := []storage.FileBaselineEntry{
		{Path: `C:\\guard\\added.txt`, Size: 10, Mode: 0o644, ModifiedUTC: modifiedUTC, ContentSHA256: "added", HashStatus: "available"},
		{Path: `C:\\guard\\changed.txt`, Size: 12, Mode: 0o644, ModifiedUTC: modifiedUTC.Add(time.Second), ContentSHA256: "new", HashStatus: "available"},
		{Path: `C:\\guard\\unchanged.txt`, Size: 10, Mode: 0o644, ModifiedUTC: modifiedUTC, ContentSHA256: "same", HashStatus: "available"},
	}

	differences := fileBaselineDifferences(before, after)
	if len(differences) != 3 {
		t.Fatalf("file baseline differences = %#v", differences)
	}
	if differences[0].Action != "file_added" || differences[0].Path != `C:\\guard\\added.txt` ||
		differences[1].Action != "file_modified" || differences[1].Path != `C:\\guard\\changed.txt` ||
		differences[2].Action != "file_removed" || differences[2].Path != `C:\\guard\\removed.txt` {
		t.Fatalf("file baseline difference classifications = %#v", differences)
	}
}

func TestFileBaselineRecoveryDoesNotReintroduceRemovedBaselineDifferences(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	root := t.TempDir()
	changedPath := filepath.Join(root, "changed.txt")
	removedPath := filepath.Join(root, "removed.txt")
	if err := os.WriteFile(changedPath, []byte("before"), 0o600); err != nil {
		t.Fatalf("write changed baseline file: %v", err)
	}
	if err := os.WriteFile(removedPath, []byte("removed"), 0o600); err != nil {
		t.Fatalf("write removed baseline file: %v", err)
	}
	if err := runtime.repository.SaveMonitoringTargets(context.Background(), []domain.MonitoringTarget{{
		Path: root, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}); err != nil {
		t.Fatalf("SaveMonitoringTargets() error = %v", err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-file-recovery", Name: "文件恢复差异测试",
	})
	baseline, err := collector.SnapshotMonitoringFileBaseline(context.Background(), []domain.MonitoringTarget{{
		Path: root, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, nil)
	if err != nil {
		t.Fatalf("SnapshotMonitoringFileBaseline() error = %v", err)
	}
	if err := runtime.repository.StoreFileBaseline(context.Background(), "session-file-recovery", storage.FileBaselineStageStart, storageFileBaselineEntries(baseline)); err != nil {
		t.Fatalf("StoreFileBaseline() error = %v", err)
	}
	if err := os.WriteFile(changedPath, []byte("after content"), 0o600); err != nil {
		t.Fatalf("modify monitored file: %v", err)
	}
	if err := os.Remove(removedPath); err != nil {
		t.Fatalf("remove monitored file: %v", err)
	}
	addedPath := filepath.Join(root, "added.txt")
	if err := os.WriteFile(addedPath, []byte("added"), 0o600); err != nil {
		t.Fatalf("add monitored file: %v", err)
	}
	session, err := runtime.repository.GetSession(context.Background(), "session-file-recovery")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if err := recordFileBaselineRecoveryBoundary(context.Background(), runtime.repository, session); err != nil {
		t.Fatalf("recordFileBaselineRecoveryBoundary() error = %v", err)
	}
	records, err := runtime.repository.ListEvents(context.Background(), "session-file-recovery")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	for _, record := range records {
		if record.Event.Source == "file_baseline_reconciliation" {
			t.Fatalf("removed file baseline event was recorded: %+v", record)
		}
	}
	recovery, err := runtime.repository.LoadFileBaseline(context.Background(), "session-file-recovery", storage.FileBaselineStageRecovery)
	if !errors.Is(err, storage.ErrFileBaselineNotFound) || len(recovery) != 0 {
		t.Fatalf("recovery file baseline entries=%+v error=%v, want no recovery baseline", recovery, err)
	}
}

func TestAgentActivityObservationKeepsOnlySummaryAndHashesUserSID(t *testing.T) {
	report := coreservice.AgentActivityReportRequest{
		SessionID: "session-1", WindowTitle: "财务报表 - Desktop Guard Pro", ProcessID: 101,
		ProcessImage: `C:\Program Files\Example\report.exe`, InputActivityCount: 7,
		KeyboardActivityCount: 4, MouseClickCount: 2, MouseWheelCount: 1,
		HighRiskShortcutCount: map[string]uint64{"alt_tab": 1},
		ActivityStartUTC:      time.Date(2026, time.September, 7, 2, 59, 30, 0, time.UTC),
		ActivityEndUTC:        time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC),
		ForegroundStartUTC:    time.Date(2026, time.September, 7, 2, 58, 0, 0, time.UTC),
		ForegroundEndUTC:      time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC),
		ForegroundDurationMS:  120000,
		ObservedUTC:           time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC),
	}
	client := coreservice.ClientIdentity{UserSID: runtimeTestOwnerSID, WindowsSessionID: 3}
	observation, err := agentActivityObservation(client, report)
	if err != nil {
		t.Fatalf("agentActivityObservation() error = %v", err)
	}
	if observation.Category != domain.EventCategorySystem || observation.Action != "agent_activity_summary" ||
		observation.Source != "session_agent" || observation.WindowsSessionID == nil || *observation.WindowsSessionID != 3 {
		t.Fatalf("observation metadata = %#v", observation)
	}
	wantSIDHash := sha256.Sum256([]byte(runtimeTestOwnerSID))
	if string(observation.UserSIDHash) != string(wantSIDHash[:]) {
		t.Fatalf("user SID hash = %x, want %x", observation.UserSIDHash, wantSIDHash)
	}
	var payload struct {
		WindowTitle           string            `json:"windowTitle"`
		ProcessID             uint32            `json:"processId"`
		ProcessImage          string            `json:"processImage"`
		InputActivityCount    uint64            `json:"inputActivityCount"`
		ActivityIntensity     string            `json:"activityIntensity"`
		ActivityRatePerMinute float64           `json:"activityRatePerMinute"`
		KeyboardActivityCount uint64            `json:"keyboardActivityCount"`
		MouseClickCount       uint64            `json:"mouseClickCount"`
		MouseWheelCount       uint64            `json:"mouseWheelCount"`
		HighRiskShortcutCount map[string]uint64 `json:"highRiskShortcutCount"`
		ActivityStartUTC      time.Time         `json:"activityStartUtc"`
		ActivityEndUTC        time.Time         `json:"activityEndUtc"`
		ForegroundStartUTC    time.Time         `json:"foregroundStartUtc"`
		ForegroundEndUTC      time.Time         `json:"foregroundEndUtc"`
		ForegroundDurationMS  int64             `json:"foregroundDurationMillis"`
	}
	if err := json.Unmarshal(observation.Payload, &payload); err != nil {
		t.Fatalf("decode activity payload: %v", err)
	}
	if payload.WindowTitle != report.WindowTitle || payload.ProcessID != report.ProcessID ||
		payload.ProcessImage != report.ProcessImage || payload.InputActivityCount != report.InputActivityCount ||
		payload.ActivityIntensity != "low" || payload.ActivityRatePerMinute != 14 ||
		payload.KeyboardActivityCount != report.KeyboardActivityCount || payload.MouseClickCount != report.MouseClickCount ||
		payload.HighRiskShortcutCount["alt_tab"] != 1 ||
		payload.MouseWheelCount != report.MouseWheelCount || !payload.ActivityStartUTC.Equal(report.ActivityStartUTC) ||
		!payload.ActivityEndUTC.Equal(report.ActivityEndUTC) || !payload.ForegroundStartUTC.Equal(report.ForegroundStartUTC) ||
		!payload.ForegroundEndUTC.Equal(report.ForegroundEndUTC) || payload.ForegroundDurationMS != report.ForegroundDurationMS {
		t.Fatalf("activity payload = %#v, want summary %#v", payload, report)
	}
	if strings.Contains(string(observation.Payload), "keyboardText") || strings.Contains(string(observation.Payload), "keyText") ||
		strings.Contains(string(observation.Payload), "keyStroke") {
		t.Fatalf("activity payload includes keyboard content: %s", observation.Payload)
	}
}

func TestAgentInputShieldObservationClassifiesEventsAndSkipsHeartbeat(t *testing.T) {
	now := time.Date(2026, time.September, 15, 14, 0, 0, 0, time.UTC)
	client := coreservice.ClientIdentity{UserSID: runtimeTestOwnerSID, WindowsSessionID: 3}
	if _, record, err := agentInputShieldObservation(client, coreservice.AgentInputShieldReportRequest{
		SessionID: "session-1", Action: "heartbeat", State: "protecting", HookRunning: true, ObservedUTC: now,
	}); err != nil || record {
		t.Fatalf("heartbeat record = %v, error = %v", record, err)
	}
	health, record, err := agentInputShieldObservation(client, coreservice.AgentInputShieldReportRequest{
		SessionID: "session-1", Action: "hook_degraded", State: "degraded", ObservedUTC: now, DroppedEvents: 3,
	})
	if err != nil || !record {
		t.Fatalf("hook degraded record = %v, error = %v", record, err)
	}
	if health.Category != domain.EventCategoryHealth || health.Action != "input_shield_hook_degraded" ||
		health.Severity != domain.EventSeverityHigh || health.Source != "session_agent_input_shield" {
		t.Fatalf("hook degraded observation = %+v", health)
	}
	device, record, err := agentInputShieldObservation(client, coreservice.AgentInputShieldReportRequest{
		SessionID: "session-1", Action: "device_connected", State: "protecting", HookRunning: true,
		Device: &coreservice.AgentInputShieldDevice{Kind: "keyboard", InstanceID: `HID\VID_1234`}, ObservedUTC: now,
	})
	if err != nil || !record || device.Category != domain.EventCategoryDevice || device.ObjectKey != `HID\VID_1234` {
		t.Fatalf("device observation = %+v, record = %v, error = %v", device, record, err)
	}
	if _, record, err := agentInputShieldObservation(client, coreservice.AgentInputShieldReportRequest{
		SessionID: "session-1", Action: "device_removed", State: "protecting", HookRunning: true, ObservedUTC: now,
	}); err != nil || record {
		t.Fatalf("filtered device record = %v, error = %v", record, err)
	}
}

func TestInputActivityIntensityUsesWindowRate(t *testing.T) {
	start := time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		count uint64
		end   time.Time
		want  string
	}{
		{0, start.Add(time.Minute), "none"},
		{20, start.Add(time.Minute), "low"},
		{60, start.Add(time.Minute), "medium"},
		{150, start.Add(time.Minute), "high"},
	} {
		intensity, _ := inputActivityIntensity(test.count, start, test.end)
		if intensity != test.want {
			t.Errorf("inputActivityIntensity(%d) = %q, want %q", test.count, intensity, test.want)
		}
	}
}

func TestInputActivityPreferenceRemovesInputMetadataAndKeepsForegroundTimeline(t *testing.T) {
	now := time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC)
	report := coreservice.AgentActivityReportRequest{
		SessionID: "session-1", WindowTitle: "编辑器", ProcessID: 101,
		InputActivityCount: 9, KeyboardActivityCount: 5, MouseClickCount: 3, MouseWheelCount: 1,
		HighRiskShortcutCount: map[string]uint64{"task_manager": 1},
		ActivityStartUTC:      now.Add(-time.Second), ActivityEndUTC: now,
		ForegroundStartUTC: now.Add(-time.Minute), ForegroundEndUTC: now, ForegroundDurationMS: 60000,
		ObservedUTC: now,
	}
	filtered, err := applyInputActivityPreference(context.Background(), inputActivityPreferenceStub(false), report)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.InputActivityCount != 0 || filtered.KeyboardActivityCount != 0 || filtered.MouseClickCount != 0 ||
		filtered.MouseWheelCount != 0 || len(filtered.HighRiskShortcutCount) != 0 ||
		!filtered.ActivityStartUTC.IsZero() || !filtered.ActivityEndUTC.IsZero() {
		t.Fatalf("input metadata was retained: %#v", filtered)
	}
	if filtered.WindowTitle != report.WindowTitle || !filtered.ForegroundStartUTC.Equal(report.ForegroundStartUTC) ||
		filtered.ForegroundDurationMS != report.ForegroundDurationMS {
		t.Fatalf("foreground timeline was changed: %#v", filtered)
	}
}

func TestHighRiskShortcutsPreferenceRequiresExplicitEnablement(t *testing.T) {
	report := coreservice.AgentActivityReportRequest{
		HighRiskShortcutCount: map[string]uint64{"win_lock": 1},
	}
	filtered, err := applyHighRiskShortcutsPreference(context.Background(), highRiskShortcutsPreferenceStub(false), report)
	if err != nil || len(filtered.HighRiskShortcutCount) != 0 {
		t.Fatalf("disabled shortcut filter=%v error=%v", filtered.HighRiskShortcutCount, err)
	}
	filtered, err = applyHighRiskShortcutsPreference(context.Background(), highRiskShortcutsPreferenceStub(true), report)
	if err != nil || filtered.HighRiskShortcutCount["win_lock"] != 1 {
		t.Fatalf("enabled shortcut filter=%v error=%v", filtered.HighRiskShortcutCount, err)
	}
}

type highRiskShortcutsPreferenceStub bool

func (stub highRiskShortcutsPreferenceStub) LoadHighRiskShortcutsEnabled(context.Context) (bool, error) {
	return bool(stub), nil
}

type inputActivityPreferenceStub bool

func (stub inputActivityPreferenceStub) LoadInputActivityEnabled(context.Context) (bool, error) {
	return bool(stub), nil
}

func TestWindowTitlePreferenceRemovesSensitiveTitleAndKeepsProcess(t *testing.T) {
	report := coreservice.AgentActivityReportRequest{
		SessionID: "session-1", WindowTitle: "客户名单.xlsx", ProcessID: 101,
		ProcessImage: `C:\Apps\sheet.exe`, ObservedUTC: time.Now().UTC(),
	}
	filtered, err := applyWindowTitlePreference(context.Background(), windowTitlePreferenceStub(false), report)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.WindowTitle != "" || filtered.ProcessID != report.ProcessID || filtered.ProcessImage != report.ProcessImage {
		t.Fatalf("window title privacy filter result = %#v", filtered)
	}
}

type windowTitlePreferenceStub bool

func (stub windowTitlePreferenceStub) LoadWindowTitleEnabled(context.Context) (bool, error) {
	return bool(stub), nil
}

func TestRetentionSchedulerPrunesUnlockedTerminalSessionsOnStartup(t *testing.T) {
	now := time.Date(2026, time.September, 7, 6, 0, 0, 0, time.UTC)
	pruner := &testRetentionPruner{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runRetentionScheduler(ctx, pruner, func() time.Time { return now }, time.Hour) }()
	select {
	case <-pruner.started:
	case <-time.After(time.Second):
		t.Fatal("retention scheduler did not run at startup")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runRetentionScheduler() error = %v", err)
	}
	if pruner.calls != 1 || pruner.limit != automaticRetentionPruneLimit || pruner.before != now.Add(-automaticRetentionPeriod) {
		t.Fatalf("retention calls=%d before=%s limit=%d", pruner.calls, pruner.before, pruner.limit)
	}
}

type testRetentionPruner struct {
	calls   int
	before  time.Time
	limit   int
	started chan struct{}
}

func (pruner *testRetentionPruner) PruneTerminalSessions(_ context.Context, before time.Time, limit int) ([]string, error) {
	pruner.calls++
	pruner.before, pruner.limit = before, limit
	close(pruner.started)
	return nil, nil
}

func TestApplicationRuntimeRejectsTamperedAuditData(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openApplicationRuntime() first error = %v", err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-1", Name: "篡改测试",
	})
	event := domain.AuditEvent{
		EventID:        "event-1",
		SessionID:      "session-1",
		Category:       domain.EventCategoryFile,
		Action:         "created",
		Severity:       domain.EventSeverityLow,
		ObservedUTC:    time.Now().UTC(),
		MonotonicTicks: 1,
		Source:         "test",
		Confidence:     domain.EventConfidenceDirect,
	}
	if _, err := runtime.repository.AppendEventAutoSequence(context.Background(), event, []byte("payload")); err != nil {
		t.Fatalf("AppendEventAutoSequence() error = %v", err)
	}
	if _, err := runtime.database.ExecContext(context.Background(), "UPDATE audit_events SET action = 'deleted' WHERE event_id = ?", event.EventID); err != nil {
		t.Fatalf("tamper audit event: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if runtime != nil {
		_ = runtime.Close()
	}
	if !errors.Is(err, storage.ErrEventIntegrity) {
		t.Fatalf("openApplicationRuntime() error = %v, want %v", err, storage.ErrEventIntegrity)
	}
}

func TestApplicationRuntimeAnalysisAndReportFlow(t *testing.T) {
	t.Parallel()

	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("openApplicationRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "session-analysis", Name: "分析集成测试",
	})
	event := domain.AuditEvent{
		EventID: "device-event", SessionID: "session-analysis",
		Category: domain.EventCategoryDevice, Action: "device_connected",
		Severity: domain.EventSeverityMedium, ObservedUTC: time.Now().UTC(),
		ObjectKey: `USB\VID_1234`, Source: "windows_device_snapshot",
		Confidence: domain.EventConfidenceDirect,
	}
	if _, err := runtime.repository.AppendEventAutoSequence(context.Background(), event, []byte(`{"instanceId":"USB\\VID_1234"}`)); err != nil {
		t.Fatalf("AppendEventAutoSequence() error = %v", err)
	}

	riskResponse := callRuntimeAPI(t, runtime, contracts.MessageTypeRiskEvaluate, coreservice.RiskEvaluateRequest{SessionID: "session-analysis"})
	if riskResponse.Type != contracts.MessageTypeRiskResult {
		t.Fatalf("risk response type = %q", riskResponse.Type)
	}
	timelineResponse := callRuntimeAPI(t, runtime, contracts.MessageTypeTimelineQuery, coreservice.TimelineQueryRequest{
		SessionID: "session-analysis", Limit: 10,
	})
	if timelineResponse.Type != contracts.MessageTypeTimelineResult {
		t.Fatalf("timeline response type = %q", timelineResponse.Type)
	}
	reportResponse := callRuntimeAPI(t, runtime, contracts.MessageTypeReportExport, coreservice.ReportExportRequest{
		SessionID: "session-analysis", Format: coreservice.ReportFormatMarkdown,
	})
	var report coreservice.ReportResult
	if err := reportResponse.DecodePayload(&report); err != nil {
		t.Fatalf("decode report result: %v", err)
	}
	if reportResponse.Type != contracts.MessageTypeReportResult || report.MediaType != "text/markdown; charset=utf-8" ||
		!strings.Contains(report.Content, "Desktop Guard Pro 保护报告") {
		t.Fatalf("unexpected report response: type=%q media=%q", reportResponse.Type, report.MediaType)
	}
}

func callRuntimeAPI(t *testing.T, runtime *applicationRuntime, messageType contracts.MessageType, payload any) contracts.Message {
	t.Helper()

	request, err := contracts.NewMessage("runtime-test", messageType, time.Now().UTC().Add(time.Minute), payload)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	response, err := runtime.api.HandleForClient(request, coreservice.ClientIdentity{UserSID: runtimeTestOwnerSID, WindowsSessionID: 1})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response.Type == contracts.MessageTypeError {
		var result coreservice.ErrorResult
		if err := json.Unmarshal(response.Payload, &result); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		t.Fatalf("Handle() response error = %s: %s", result.Code, result.Message)
	}
	return response
}

func findRuntimeEvent(records []storage.EventRecord, action string) (storage.EventRecord, bool) {
	for _, record := range records {
		if record.Event.Action == action {
			return record, true
		}
	}
	return storage.EventRecord{}, false
}

func openTestApplicationRuntime(ctx context.Context, dataDirectory string) (*applicationRuntime, error) {
	policy, err := installpolicy.New(runtimeTestOwnerSID)
	if err != nil {
		return nil, err
	}
	if err := installpolicy.EnsureOwner(dataDirectory, policy); err != nil {
		return nil, err
	}
	return openApplicationRuntime(ctx, dataDirectory)
}

func TestApplicationRuntimePersistsInputShieldCredentials(t *testing.T) {
	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("openTestApplicationRuntime() error = %v", err)
	}
	updated := callRuntimeAPI(t, runtime, contracts.MessageTypeInputShieldCredentialUpdate,
		coreservice.InputShieldCredentialUpdateRequest{Password: []byte("runtime-password"), EnableRecovery: true})
	var updateResult coreservice.InputShieldCredentialResult
	if err := updated.DecodePayload(&updateResult); err != nil {
		t.Fatalf("decode credential update: %v", err)
	}
	if !updateResult.Configured || !updateResult.RecoveryCodeEnabled || updateResult.RecoveryCode == "" {
		t.Fatalf("credential update result = %+v", updateResult)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatalf("reopen application runtime: %v", err)
	}
	defer runtime.Close()
	status := callRuntimeAPI(t, runtime, contracts.MessageTypeInputShieldCredentialGet, struct{}{})
	var statusResult coreservice.InputShieldCredentialResult
	if err := status.DecodePayload(&statusResult); err != nil {
		t.Fatalf("decode credential status: %v", err)
	}
	if !statusResult.Configured || !statusResult.RecoveryCodeEnabled || statusResult.RecoveryCode != "" {
		t.Fatalf("credential status result = %+v", statusResult)
	}
}

func TestApplicationRuntimeRequiresInstallPolicy(t *testing.T) {
	runtime, err := openApplicationRuntime(context.Background(), t.TempDir())
	if runtime != nil {
		_ = runtime.Close()
	}
	if !errors.Is(err, installpolicy.ErrPolicyInvalid) {
		t.Fatalf("openApplicationRuntime() error = %v, want %v", err, installpolicy.ErrPolicyInvalid)
	}
}

func TestHistoricalSessionAnalysisAfterRestartWithAnotherCurrentSession(t *testing.T) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	runtime, err := openTestApplicationRuntime(ctx, dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if runtime != nil {
			_ = runtime.Close()
		}
	}()
	archived, err := domain.NewSession("history-a", "Archived session A")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.repository.CreateSession(ctx, *archived, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStateFinalizing, domain.SessionStateCompleted} {
		if err := archived.Transition(state); err != nil {
			t.Fatal(err)
		}
		if err := runtime.repository.UpdateSession(ctx, *archived, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	event := domain.AuditEvent{
		EventID: "history-a-evidence", SessionID: archived.ID, Category: domain.EventCategoryDevice,
		Action: "device_connected", Severity: domain.EventSeverityMedium, ObservedUTC: time.Now().UTC(),
		ObjectKey: "archived-device-a", Source: "test", Confidence: domain.EventConfidenceDirect,
	}
	if _, err := runtime.repository.AppendEventAutoSequence(ctx, event, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = openTestApplicationRuntime(ctx, dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.coordinator.Current(); ok {
		t.Fatal("terminal session became current after restart")
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{ID: "current-b", Name: "Current session B"})
	var history storage.SessionListPage
	if err := callRuntimeAPI(t, runtime, contracts.MessageTypeSessionList, coreservice.SessionListRequest{}).DecodePayload(&history); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range history.Items {
		if item.Session.ID == archived.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("archive missing from history")
	}
	var timeline storage.TimelinePage
	if err := callRuntimeAPI(t, runtime, contracts.MessageTypeTimelineQuery, coreservice.TimelineQueryRequest{SessionID: archived.ID, Limit: 100}).DecodePayload(&timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Records) != 1 || timeline.Records[0].Event.EventID != event.EventID {
		t.Fatal("timeline queried wrong session")
	}
	for _, query := range []struct {
		kind    contracts.MessageType
		payload any
	}{
		{contracts.MessageTypeRiskEvaluate, coreservice.RiskEvaluateRequest{SessionID: archived.ID}},
		{contracts.MessageTypeAssetDifferenceQuery, coreservice.AssetDifferenceQueryRequest{SessionID: archived.ID}},
	} {
		response := callRuntimeAPI(t, runtime, query.kind, query.payload)
		var result struct {
			SessionID string `json:"sessionId"`
		}
		if err := response.DecodePayload(&result); err != nil || result.SessionID != archived.ID {
			t.Fatalf("wrong analysis session: %s %v", response.Payload, err)
		}
	}
	var report coreservice.ReportResult
	if err := callRuntimeAPI(t, runtime, contracts.MessageTypeReportExport, coreservice.ReportExportRequest{SessionID: archived.ID, Format: coreservice.ReportFormatJSON, FromSequence: 1, ToSequence: 1}).DecodePayload(&report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.Content, "history-a-evidence") || strings.Contains(report.Content, "current-b") {
		t.Fatal("report mixed historical and current sessions")
	}
}
