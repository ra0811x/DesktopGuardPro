package storage

import (
	"bytes"
	"context"
	"desktopguardpro/internal/domain"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestReportRangeRetainsSelectedEventsAndVerifiesWholeChain(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "range.db"), bytes.Repeat([]byte{0x74}, payloadKeySize))
	defer db.Close()
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 5)
	records, total, err := repository.ListEventsRange(context.Background(), "session-1", 2, 4)
	if err != nil || total != 5 || len(records) != 3 || records[0].Event.Sequence != 2 || records[2].Event.Sequence != 4 {
		t.Fatalf("range: %d, %d, %v", total, len(records), err)
	}
	if _, err := db.Exec("UPDATE audit_events SET action = 'tampered' WHERE sequence = 1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ListEventsRange(context.Background(), "session-1", 2, 4); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("range ignored damage elsewhere: %v", err)
	}
}

func TestReportRangeUsesLatestStatusOutsideSelectedEvidence(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "status.db"), bytes.Repeat([]byte{0x75}, payloadKeySize))
	defer db.Close()
	createTestSession(t, repository)
	appendTimelineEvents(t, repository, 2)
	for index, status := range []string{"known", "action_needed"} {
		payload, _ := json.Marshal(map[string]string{"findingId": "finding-1", "status": status})
		_, err := repository.AppendEventAutoSequence(context.Background(), domain.AuditEvent{EventID: status, SessionID: "session-1", Category: domain.EventCategorySystem, Action: "risk_finding_status_changed", Severity: domain.EventSeverityLow, ObservedUTC: time.Now().UTC().Add(time.Duration(index) * time.Second), Source: "test", Confidence: domain.EventConfidenceDirect}, payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	records, total, statuses, err := repository.ListEventsRangeWithFindingStatuses(context.Background(), "session-1", 1, 1)
	if err != nil || len(records) != 1 || total != 4 || statuses["finding-1"] != "action_needed" {
		t.Fatalf("range=%+v total=%d statuses=%v err=%v", records, total, statuses, err)
	}
	if _, err := db.Exec("UPDATE audit_events SET action = 'tampered' WHERE sequence = 4"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repository.ListEventsRangeWithFindingStatuses(context.Background(), "session-1", 1, 1); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("status integrity bypass: %v", err)
	}
}
