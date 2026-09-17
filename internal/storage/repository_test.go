package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestRepositoryPersistsSessionAcrossRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "guard.db")
	key := bytes.Repeat([]byte{0x31}, payloadKeySize)
	db, repository := openTestRepository(t, path, key)

	session, err := domain.NewSessionWithMonitoringLevel("session-1", "Baseline", domain.MonitoringLevelStrict)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	createdAt := time.Date(2026, 8, 23, 2, 3, 4, 0, time.UTC)
	if err := repository.CreateSession(context.Background(), *session, createdAt); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := session.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if err := repository.UpdateSession(context.Background(), *session, createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateSession() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db, repository = openTestRepository(t, path, key)
	t.Cleanup(func() { _ = db.Close() })
	got, err := repository.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.ID != session.ID || got.Name != session.Name || got.State != session.State ||
		got.Revision != session.Revision || got.MonitoringLevel != session.MonitoringLevel {
		t.Fatalf("GetSession() = %+v, want %+v", got, *session)
	}
}

func TestOpenMigratesVersionSixDatabaseForBaselineReviewState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "guard.db")
	legacy, err := sql.Open("sqlite", databaseSourceName(path))
	if err != nil {
		t.Fatalf("open version six database: %v", err)
	}
	for index, migration := range migrations[:6] {
		if _, err := legacy.Exec(migration); err != nil {
			_ = legacy.Close()
			t.Fatalf("apply migration %d: %v", index+1, err)
		}
		if _, err := legacy.Exec(fmt.Sprintf("PRAGMA user_version = %d", index+1)); err != nil {
			_ = legacy.Close()
			t.Fatalf("record migration %d: %v", index+1, err)
		}
	}
	if _, err := legacy.Exec(`
		INSERT INTO sessions (id, name, state, revision, created_utc, updated_utc)
		VALUES ('session-legacy', 'Legacy Session', 'preparing', 1, '2026-09-10T00:00:00Z', '2026-09-10T00:00:00Z')
	`); err != nil {
		_ = legacy.Close()
		t.Fatalf("insert version six session: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close version six database: %v", err)
	}

	db, repository := openTestRepository(t, path, bytes.Repeat([]byte{0x35}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	session, err := repository.GetSession(context.Background(), "session-legacy")
	if err != nil {
		t.Fatalf("GetSession() after migration error = %v", err)
	}
	if err := session.Transition(domain.SessionStateBaselineReview); err != nil {
		t.Fatalf("Transition(baseline review) error = %v", err)
	}
	if err := repository.UpdateSession(context.Background(), session, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateSession(baseline review) error = %v", err)
	}
	if session.MonitoringLevel != domain.MonitoringLevelStandard {
		t.Fatalf("legacy session monitoring level = %q, want %q", session.MonitoringLevel, domain.MonitoringLevelStandard)
	}
}

func TestRepositoryStoresAssetBaselinesBySessionAndStage(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x32}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	second, err := domain.NewSession("session-2", "Second Session")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := repository.CreateSession(context.Background(), *second, time.Now().UTC()); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	firstStart := domain.AssetBaseline{Assets: []domain.Asset{{
		Category: domain.AssetCategorySoftware, Identifier: "app-a", DisplayName: "应用 A",
		Attributes: map[string]string{"version": "1.0", "publisher": "Contoso"},
	}}}
	if err := repository.StoreAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart, firstStart); err != nil {
		t.Fatalf("StoreAssetBaseline() first start error = %v", err)
	}
	if err := repository.StoreAssetBaseline(context.Background(), "session-1", AssetBaselineStageEnd, domain.AssetBaseline{Assets: []domain.Asset{{
		Category: domain.AssetCategorySoftware, Identifier: "app-a", DisplayName: "应用 A",
		Attributes: map[string]string{"version": "2.0", "publisher": "Contoso"},
	}}}); err != nil {
		t.Fatalf("StoreAssetBaseline() end error = %v", err)
	}
	if err := repository.StoreAssetBaseline(context.Background(), "session-2", AssetBaselineStageStart, domain.AssetBaseline{Assets: []domain.Asset{{
		Category: domain.AssetCategoryDevice, Identifier: "usb-1", DisplayName: "存储设备",
	}}}); err != nil {
		t.Fatalf("StoreAssetBaseline() second session error = %v", err)
	}

	gotStart, err := repository.LoadAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart)
	if err != nil {
		t.Fatalf("LoadAssetBaseline() start error = %v", err)
	}
	if !reflect.DeepEqual(gotStart, firstStart) {
		t.Fatalf("LoadAssetBaseline() start = %#v, want %#v", gotStart, firstStart)
	}
	gotEnd, err := repository.LoadAssetBaseline(context.Background(), "session-1", AssetBaselineStageEnd)
	if err != nil {
		t.Fatalf("LoadAssetBaseline() end error = %v", err)
	}
	if gotEnd.Assets[0].Attributes["version"] != "2.0" {
		t.Fatalf("end baseline = %#v, want version 2.0", gotEnd)
	}
	secondStart, err := repository.LoadAssetBaseline(context.Background(), "session-2", AssetBaselineStageStart)
	if err != nil {
		t.Fatalf("LoadAssetBaseline() second session error = %v", err)
	}
	if len(secondStart.Assets) != 1 || secondStart.Assets[0].Identifier != "usb-1" {
		t.Fatalf("second session baseline = %#v", secondStart)
	}

	replacement := domain.AssetBaseline{Assets: []domain.Asset{{
		Category: domain.AssetCategoryNetwork, Identifier: "adapter-1", DisplayName: "以太网",
		Attributes: map[string]string{"dns": "1.1.1.1"},
	}}}
	if err := repository.StoreAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart, replacement); err != nil {
		t.Fatalf("StoreAssetBaseline() replacement error = %v", err)
	}
	gotReplacement, err := repository.LoadAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart)
	if err != nil {
		t.Fatalf("LoadAssetBaseline() replacement error = %v", err)
	}
	if !reflect.DeepEqual(gotReplacement, replacement) {
		t.Fatalf("replacement baseline = %#v, want %#v", gotReplacement, replacement)
	}
}

func TestRepositoryStoresUSNJournalCheckpointsBySessionAndVolume(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x36}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	first := USNJournalCheckpoint{SessionID: "session-1", Stage: USNCheckpointStageStart, Volume: `C:\`, JournalID: 101, NextUSN: 202, CapturedUTC: time.Now().UTC()}
	second := USNJournalCheckpoint{SessionID: "session-1", Stage: USNCheckpointStageStart, Volume: `D:\`, JournalID: 303, NextUSN: 404, CapturedUTC: time.Now().UTC()}
	if err := repository.StoreUSNJournalCheckpoints(context.Background(), []USNJournalCheckpoint{first, second}); err != nil {
		t.Fatalf("StoreUSNJournalCheckpoints() error = %v", err)
	}
	updated := first
	updated.NextUSN = 505
	if err := repository.StoreUSNJournalCheckpoints(context.Background(), []USNJournalCheckpoint{updated}); err != nil {
		t.Fatalf("StoreUSNJournalCheckpoints(update) error = %v", err)
	}
	checkpoints, err := repository.LoadUSNJournalCheckpoints(context.Background(), "session-1", USNCheckpointStageStart)
	if err != nil {
		t.Fatalf("LoadUSNJournalCheckpoints() error = %v", err)
	}
	if len(checkpoints) != 2 || checkpoints[0].Volume != `C:\` || checkpoints[0].NextUSN != 505 || checkpoints[1].Volume != `D:\` {
		t.Fatalf("checkpoints = %#v", checkpoints)
	}
}

func TestRepositoryStoresFileBaselinesBySessionAndStage(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x37}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	now := time.Now().UTC()
	entries := []FileBaselineEntry{
		{Path: `C:\Evidence\large.bin`, Size: 100, Mode: 0o600, CreatedUTC: now.Add(-time.Hour), AccessedUTC: now.Add(-time.Minute), ModifiedUTC: now, HashStatus: "skipped_size_limit"},
		{Path: `C:\Evidence\report.txt`, Size: 12, Mode: 0o600, CreatedUTC: now.Add(-time.Hour), AccessedUTC: now.Add(-time.Minute), ModifiedUTC: now, ContentSHA256: "abc", HashStatus: "available"},
	}
	if err := repository.StoreFileBaseline(context.Background(), "session-1", FileBaselineStageStart, entries); err != nil {
		t.Fatalf("StoreFileBaseline() error = %v", err)
	}
	loaded, err := repository.LoadFileBaseline(context.Background(), "session-1", FileBaselineStageStart)
	if err != nil {
		t.Fatalf("LoadFileBaseline() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, entries) {
		t.Fatalf("loaded file baseline = %#v, want %#v", loaded, entries)
	}
}

func TestRepositoryDistinguishesMissingAndEmptyAssetBaselines(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x33}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)

	if _, err := repository.LoadAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart); !errors.Is(err, ErrAssetBaselineNotFound) {
		t.Fatalf("LoadAssetBaseline() error = %v, want %v", err, ErrAssetBaselineNotFound)
	}
	if err := repository.StoreAssetBaseline(context.Background(), "missing", AssetBaselineStageStart, domain.AssetBaseline{}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("StoreAssetBaseline() missing session error = %v, want %v", err, ErrSessionNotFound)
	}
	if err := repository.StoreAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart, domain.AssetBaseline{}); err != nil {
		t.Fatalf("StoreAssetBaseline() empty baseline error = %v", err)
	}

	emptyBaseline, err := repository.LoadAssetBaseline(context.Background(), "session-1", AssetBaselineStageStart)
	if err != nil {
		t.Fatalf("LoadAssetBaseline() empty baseline error = %v", err)
	}
	if len(emptyBaseline.Assets) != 0 {
		t.Fatalf("empty baseline assets = %#v, want no assets", emptyBaseline.Assets)
	}
}

func TestRepositoryPersistsBaselineReviewAndResolution(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x34}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	result := domain.BaselineCaptureResult{
		AttemptedItemCount: 3,
		SucceededItemCount: 2,
		Failures: []domain.BaselineCaptureFailure{{
			Item:   "Wi-Fi SSID",
			Reason: "access denied",
		}},
	}

	if err := repository.StoreBaselineReview(context.Background(), "session-1", result); err != nil {
		t.Fatalf("StoreBaselineReview() error = %v", err)
	}
	review, err := repository.LoadBaselineReview(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("LoadBaselineReview() error = %v", err)
	}
	if review.Decision.Status != domain.BaselineCaptureStatusPartialFailure ||
		!review.Decision.RequiresUserChoice || review.Resolution != "" ||
		!reflect.DeepEqual(review.Decision.Failures, result.Failures) {
		t.Fatalf("review = %#v", review)
	}

	if err := repository.ResolveBaselineReview(context.Background(), "session-1", BaselineReviewResolutionContinue); err != nil {
		t.Fatalf("ResolveBaselineReview() error = %v", err)
	}
	review, err = repository.LoadBaselineReview(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("LoadBaselineReview() after resolution error = %v", err)
	}
	if review.Resolution != BaselineReviewResolutionContinue {
		t.Fatalf("resolution = %q, want %q", review.Resolution, BaselineReviewResolutionContinue)
	}

	if err := repository.StoreBaselineReview(context.Background(), "missing", result); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("StoreBaselineReview() missing session error = %v, want %v", err, ErrSessionNotFound)
	}
	if _, err := repository.LoadBaselineReview(context.Background(), "missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("LoadBaselineReview() missing session error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestRepositoryEncryptsAndVerifiesEventChain(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x42}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)

	first := testAuditEvent(1)
	firstPayload := []byte(`{"path":"C:\\secret.txt"}`)
	storedFirst, err := repository.AppendEvent(context.Background(), first, firstPayload)
	if err != nil {
		t.Fatalf("AppendEvent() first error = %v", err)
	}
	second := testAuditEvent(2)
	second.EventID = "event-2"
	second.Action = "modified"
	secondPayload := []byte(`{"size":128}`)
	storedSecond, err := repository.AppendEvent(context.Background(), second, secondPayload)
	if err != nil {
		t.Fatalf("AppendEvent() second error = %v", err)
	}
	if !bytes.Equal(storedSecond.PreviousHash, storedFirst.EventHash) {
		t.Fatal("second event does not reference the first event hash")
	}

	var rawCiphertext []byte
	if err := db.QueryRowContext(context.Background(), "SELECT payload_ciphertext FROM audit_events WHERE event_id = ?", first.EventID).Scan(&rawCiphertext); err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}
	if bytes.Contains(rawCiphertext, firstPayload) {
		t.Fatal("database ciphertext contains plaintext payload")
	}

	records, err := repository.ListEvents(context.Background(), first.SessionID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("ListEvents() count = %d, want 2", len(records))
	}
	if !bytes.Equal(records[0].Payload, firstPayload) || !bytes.Equal(records[1].Payload, secondPayload) {
		t.Fatalf("ListEvents() returned unexpected plaintext payloads")
	}
}

func TestRepositoryAutomaticallyAllocatesConcurrentEventSequences(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x43}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)

	const eventCount = 12
	var writers sync.WaitGroup
	errorsByWriter := make(chan error, eventCount)
	for index := 0; index < eventCount; index++ {
		writers.Add(1)
		go func(index int) {
			defer writers.Done()
			event := testAuditEvent(1)
			event.EventID = fmt.Sprintf("automatic-event-%d", index)
			event.Sequence = 0
			if _, err := repository.AppendEventAutoSequence(context.Background(), event, nil); err != nil {
				errorsByWriter <- err
			}
		}(index)
	}
	writers.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		t.Fatalf("AppendEventAutoSequence() error = %v", err)
	}

	records, err := repository.ListEvents(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) != eventCount {
		t.Fatalf("record count = %d, want %d", len(records), eventCount)
	}
	for index, record := range records {
		if record.Event.Sequence != uint64(index+1) {
			t.Fatalf("record[%d] sequence = %d, want %d", index, record.Event.Sequence, index+1)
		}
	}
}

func TestRepositoryAtomicallyUpdatesSessionAndAppendsLifecycleEvent(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x44}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	session := createTestSession(t, repository)
	if err := session.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	invalidEvent := testAuditEvent(0)
	invalidEvent.Action = ""
	if _, err := repository.UpdateSessionAndAppendEvent(context.Background(), *session, time.Now().UTC(), invalidEvent, nil); err == nil {
		t.Fatal("UpdateSessionAndAppendEvent() error = nil for invalid event")
	}
	persisted, err := repository.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession() after rollback error = %v", err)
	}
	if persisted.State != domain.SessionStateDraft || persisted.Revision != 0 {
		t.Fatalf("session after rollback = %+v", persisted)
	}

	event := testAuditEvent(0)
	event.EventID = "lifecycle-event"
	event.Action = "protection_preparing"
	stored, err := repository.UpdateSessionAndAppendEvent(context.Background(), *session, time.Now().UTC(), event, []byte(`{"currentState":"preparing"}`))
	if err != nil {
		t.Fatalf("UpdateSessionAndAppendEvent() error = %v", err)
	}
	if stored.Sequence != 1 {
		t.Fatalf("stored event sequence = %d, want 1", stored.Sequence)
	}
	persisted, err = repository.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession() after commit error = %v", err)
	}
	if persisted != *session {
		t.Fatalf("persisted session = %+v, want %+v", persisted, *session)
	}
	records, err := repository.ListEvents(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) != 1 || records[0].Event.Action != "protection_preparing" {
		t.Fatalf("lifecycle event records = %+v", records)
	}
}

func TestRepositoryAtomicallyResolvesBaselineReviewAndUpdatesSession(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x45}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	session := createTestSession(t, repository)
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateBaselineReview} {
		if err := session.Transition(state); err != nil {
			t.Fatalf("Transition(%s) error = %v", state, err)
		}
		if err := repository.UpdateSession(context.Background(), *session, time.Now().UTC()); err != nil {
			t.Fatalf("UpdateSession(%s) error = %v", state, err)
		}
	}
	if err := repository.StoreBaselineReview(context.Background(), session.ID, domain.BaselineCaptureResult{
		AttemptedItemCount: 1,
		Failures:           []domain.BaselineCaptureFailure{{Item: `D:\Evidence`, Reason: "access denied"}},
	}); err != nil {
		t.Fatalf("StoreBaselineReview() error = %v", err)
	}
	if err := session.Transition(domain.SessionStateActive); err != nil {
		t.Fatalf("Transition(active) error = %v", err)
	}
	invalidEvent := testAuditEvent(0)
	invalidEvent.Action = ""
	if _, err := repository.ResolveBaselineReviewAndUpdateSessionAndAppendEvent(
		context.Background(), *session, time.Now().UTC(), BaselineReviewResolutionContinue, invalidEvent, nil,
	); err == nil {
		t.Fatal("ResolveBaselineReviewAndUpdateSessionAndAppendEvent() error = nil for invalid event")
	}
	review, err := repository.LoadBaselineReview(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("LoadBaselineReview() after rollback error = %v", err)
	}
	if review.Resolution != "" {
		t.Fatalf("resolution after rollback = %q", review.Resolution)
	}
	persisted, err := repository.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession() after rollback error = %v", err)
	}
	if persisted.State != domain.SessionStateBaselineReview {
		t.Fatalf("session after rollback = %+v", persisted)
	}

	event := testAuditEvent(0)
	event.EventID = "baseline-resolved-event"
	event.Action = "protection_started"
	stored, err := repository.ResolveBaselineReviewAndUpdateSessionAndAppendEvent(
		context.Background(), *session, time.Now().UTC(), BaselineReviewResolutionContinue, event, []byte(`{"resolution":"continue"}`),
	)
	if err != nil {
		t.Fatalf("ResolveBaselineReviewAndUpdateSessionAndAppendEvent() error = %v", err)
	}
	if stored.Sequence != 1 {
		t.Fatalf("stored event sequence = %d, want 1", stored.Sequence)
	}
	review, err = repository.LoadBaselineReview(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("LoadBaselineReview() after commit error = %v", err)
	}
	if review.Resolution != BaselineReviewResolutionContinue {
		t.Fatalf("resolution = %q, want continue", review.Resolution)
	}
	persisted, err = repository.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession() after commit error = %v", err)
	}
	if persisted != *session {
		t.Fatalf("session after commit = %+v, want %+v", persisted, *session)
	}
}

func TestRepositoryPersistsPausedSessionState(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x46}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	session := createTestSession(t, repository)
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStatePaused} {
		if err := session.Transition(state); err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdateSession(context.Background(), *session, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := repository.GetSession(context.Background(), session.ID)
	if err != nil || stored.State != domain.SessionStatePaused {
		t.Fatalf("paused session=%+v error=%v", stored, err)
	}
}

func TestRepositoryDetectsDatabaseTampering(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x53}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)

	event := testAuditEvent(1)
	if _, err := repository.AppendEvent(context.Background(), event, []byte("payload")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE audit_events SET action = 'deleted' WHERE event_id = ?", event.EventID); err != nil {
		t.Fatalf("tamper event: %v", err)
	}

	if _, err := repository.ListEvents(context.Background(), event.SessionID); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("ListEvents() error = %v, want %v", err, ErrEventIntegrity)
	}
}

func TestRepositoryDetectsTruncatedEventChain(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		deleteSQL  string
		deleteArgs []any
	}{
		{
			name:       "tail event",
			deleteSQL:  "DELETE FROM audit_events WHERE session_id = ? AND sequence = ?",
			deleteArgs: []any{"session-1", 3},
		},
		{
			name:       "all events",
			deleteSQL:  "DELETE FROM audit_events WHERE session_id = ?",
			deleteArgs: []any{"session-1"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x60}, payloadKeySize))
			t.Cleanup(func() { _ = db.Close() })
			createTestSession(t, repository)
			for sequence := uint64(1); sequence <= 3; sequence++ {
				event := testAuditEvent(sequence)
				event.EventID = fmt.Sprintf("event-%d", sequence)
				if _, err := repository.AppendEvent(context.Background(), event, nil); err != nil {
					t.Fatalf("AppendEvent(%d) error = %v", sequence, err)
				}
			}
			if _, err := db.ExecContext(context.Background(), testCase.deleteSQL, testCase.deleteArgs...); err != nil {
				t.Fatalf("truncate event chain: %v", err)
			}

			if _, err := repository.ListEvents(context.Background(), "session-1"); !errors.Is(err, ErrEventChain) {
				t.Fatalf("ListEvents() error = %v, want %v", err, ErrEventChain)
			}
		})
	}
}

func TestRepositoryDetectsForgedEventChainCheckpoint(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x61}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	for sequence := uint64(1); sequence <= 3; sequence++ {
		event := testAuditEvent(sequence)
		event.EventID = fmt.Sprintf("event-%d", sequence)
		if _, err := repository.AppendEvent(context.Background(), event, nil); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", sequence, err)
		}
	}

	var secondHash, originalCheckpoint []byte
	if err := db.QueryRowContext(context.Background(), "SELECT event_hash FROM audit_events WHERE session_id = ? AND sequence = ?", "session-1", 2).Scan(&secondHash); err != nil {
		t.Fatalf("query second event hash: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT tail_hash FROM event_chain_checkpoints WHERE session_id = ?", "session-1").Scan(&originalCheckpoint); err != nil {
		t.Fatalf("query event chain checkpoint: %v", err)
	}
	if len(originalCheckpoint) <= eventHashSize {
		t.Fatalf("event chain checkpoint length = %d, want authenticated checkpoint", len(originalCheckpoint))
	}
	forgedCheckpoint := append(append([]byte(nil), secondHash...), originalCheckpoint[eventHashSize:]...)
	if _, err := db.ExecContext(context.Background(), "DELETE FROM audit_events WHERE session_id = ? AND sequence = ?", "session-1", 3); err != nil {
		t.Fatalf("truncate event chain: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
		UPDATE event_chain_checkpoints
		SET event_count = ?, tail_hash = ?
		WHERE session_id = ?
	`, 2, forgedCheckpoint, "session-1"); err != nil {
		t.Fatalf("forge event chain checkpoint: %v", err)
	}

	if _, err := repository.ListEvents(context.Background(), "session-1"); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("ListEvents() error = %v, want %v", err, ErrEventIntegrity)
	}
}

func TestRepositoryRejectsSequenceGapAndStaleSessionRevision(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x64}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	session := createTestSession(t, repository)

	event := testAuditEvent(2)
	event.EventID = "event-2"
	if _, err := repository.AppendEvent(context.Background(), event, nil); !errors.Is(err, ErrEventSequenceConflict) {
		t.Fatalf("AppendEvent() error = %v, want %v", err, ErrEventSequenceConflict)
	}

	if err := session.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if err := repository.UpdateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateSession() first error = %v", err)
	}
	if err := repository.UpdateSession(context.Background(), *session, time.Now().UTC()); !errors.Is(err, ErrSessionRevisionConflict) {
		t.Fatalf("UpdateSession() stale error = %v, want %v", err, ErrSessionRevisionConflict)
	}
}

func TestRepositoryFindsRecoverableSession(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x75}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	session := createTestSession(t, repository)
	if err := session.Transition(domain.SessionStatePreparing); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if err := repository.UpdateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateSession() error = %v", err)
	}

	got, ok, err := repository.FindRecoverableSession(context.Background())
	if err != nil {
		t.Fatalf("FindRecoverableSession() error = %v", err)
	}
	if !ok {
		t.Fatal("FindRecoverableSession() ok = false, want true")
	}
	if got != *session {
		t.Fatalf("FindRecoverableSession() = %+v, want %+v", got, *session)
	}
}

func TestRepositoryReturnsLastEventSequence(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x97}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)

	sequence, err := repository.LastEventSequence(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("LastEventSequence() empty error = %v", err)
	}
	if sequence != 0 {
		t.Fatalf("LastEventSequence() empty = %d, want 0", sequence)
	}
	for index := uint64(1); index <= 2; index++ {
		event := testAuditEvent(index)
		event.EventID = fmt.Sprintf("event-%d", index)
		if _, err := repository.AppendEvent(context.Background(), event, nil); err != nil {
			t.Fatalf("AppendEvent(%d) error = %v", index, err)
		}
	}

	sequence, err = repository.LastEventSequence(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("LastEventSequence() error = %v", err)
	}
	if sequence != 2 {
		t.Fatalf("LastEventSequence() = %d, want 2", sequence)
	}
	boundarySequence, observedUTC, err := repository.LastEventBoundary(context.Background(), "session-1")
	if err != nil || boundarySequence != 2 || !observedUTC.Equal(testAuditEvent(2).ObservedUTC) {
		t.Fatalf("LastEventBoundary() = %d, %s, %v", boundarySequence, observedUTC, err)
	}
	if _, err := repository.LastEventSequence(context.Background(), "missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("LastEventSequence(missing) error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestRepositoryRejectsMultipleRecoverableSessions(t *testing.T) {
	t.Parallel()

	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{0x86}, payloadKeySize))
	t.Cleanup(func() { _ = db.Close() })
	createTestSession(t, repository)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO sessions (id, name, state, revision, created_utc, updated_utc)
		VALUES ('session-2', 'Second', 'active', 2, ?, ?)
	`, formatTimestamp(time.Now().UTC()), formatTimestamp(time.Now().UTC())); err != nil {
		t.Fatalf("insert second session: %v", err)
	}

	if _, _, err := repository.FindRecoverableSession(context.Background()); !errors.Is(err, ErrMultipleRecoverableSessions) {
		t.Fatalf("FindRecoverableSession() error = %v, want %v", err, ErrMultipleRecoverableSessions)
	}
}

func openTestRepository(t *testing.T, path string, key []byte) (*sql.DB, *Repository) {
	t.Helper()

	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	cipher, err := NewPayloadCipher(key)
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewPayloadCipher() error = %v", err)
	}
	integrity, err := NewEventIntegrity(key)
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewEventIntegrity() error = %v", err)
	}
	repository, err := NewRepository(db, cipher, integrity)
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewRepository() error = %v", err)
	}
	return db, repository
}

func createTestSession(t *testing.T, repository *Repository) *domain.Session {
	t.Helper()

	session, err := domain.NewSession("session-1", "Test Session")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := repository.CreateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return session
}
