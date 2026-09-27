package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestSessionHistoryRemainsDiscoverableAfterRestartAndPagesStably(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.db")
	key := bytes.Repeat([]byte{8}, 32)
	db, repository := openTestRepository(t, path, key)
	for index := 0; index < 53; index++ {
		session, _ := domain.NewSession(fmt.Sprintf("history-%02d", index), fmt.Sprintf("会话 %d", index))
		if err := repository.CreateSession(context.Background(), *session, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE sessions SET state='completed', revision=4"); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetSessionRetentionLock(context.Background(), "history-52", true); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, repository = openTestRepository(t, path, key)
	defer db.Close()
	first, err := repository.ListSessions(context.Background(), "")
	if err != nil || len(first.Items) != 50 || !first.HasMore || first.Items[0].Session.ID != "history-52" {
		t.Fatalf("first page=%+v, %v", first, err)
	}
	second, err := repository.ListSessions(context.Background(), first.NextCursor)
	if err != nil || len(second.Items) != 3 || second.HasMore || second.Items[2].Session.ID != "history-00" {
		t.Fatalf("second page=%+v, %v", second, err)
	}
	if second.Items[0].Session.State != domain.SessionStateCompleted || second.Items[0].CreatedUTC.IsZero() {
		t.Fatal("history lost saved session metadata")
	}
	if !first.Items[0].RetentionLocked {
		t.Fatal("history lost saved retention lock")
	}
	if first.Items[0].Session.MonitoringPolicy != domain.DefaultMonitoringPolicy() {
		t.Fatalf("history lost monitoring policy: %+v", first.Items[0].Session.MonitoringPolicy)
	}
	if _, err := repository.ListSessions(context.Background(), "-1"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}

func TestSessionHistoryPagesByEncodedBytesWithoutSkippingSessions(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{9}, 32))
	defer db.Close()
	for index := 0; index < 3; index++ {
		session, _ := domain.NewSession(fmt.Sprintf("%d-%s", index, strings.Repeat("x", 300*1024)), "Large ID")
		if err := repository.CreateSession(context.Background(), *session, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	cursor := ""
	for index := 2; index >= 0; index-- {
		page, err := repository.ListSessions(context.Background(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded) > 512*1024 || len(page.Items) != 1 {
			t.Fatalf("page items=%d, encoded bytes=%d, error=%v", len(page.Items), len(encoded), err)
		}
		if !strings.HasPrefix(page.Items[0].Session.ID, fmt.Sprintf("%d-", index)) || page.HasMore != (index > 0) {
			t.Fatal("byte pagination skipped a session or lost the continuation")
		}
		cursor = page.NextCursor
	}
}

func TestSessionHistoryRejectsSingleEntryLargerThanEncodedBudget(t *testing.T) {
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "guard.db"), bytes.Repeat([]byte{9}, 32))
	defer db.Close()
	session, _ := domain.NewSession(strings.Repeat("&", 100*1024), "Escaped ID")
	if err := repository.CreateSession(context.Background(), *session, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListSessions(context.Background(), ""); err == nil {
		t.Fatal("history accepted a single entry that exceeds the encoded response budget")
	}
}
