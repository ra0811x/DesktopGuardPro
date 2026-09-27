package storage

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveEventReadersVerifyAConsistentSnapshotWhileAppending(t *testing.T) {
	for _, reader := range []string{"timeline", "report"} {
		t.Run(reader, func(t *testing.T) {
			db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "live.db"), bytes.Repeat([]byte{0x54}, 32))
			defer db.Close()
			createTestSession(t, repository)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				for sequence := uint64(1); sequence <= 128; sequence++ {
					event := testAuditEvent(sequence)
					event.EventID = fmt.Sprintf("live-%d", sequence)
					if _, err := repository.AppendEvent(ctx, event, []byte(`{"live":true}`)); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			var readError error
			for readError == nil {
				if reader == "timeline" {
					_, readError = repository.QueryTimeline(ctx, TimelineQuery{SessionID: "session-1", Limit: 10})
				} else {
					_, readError = repository.ListEvents(ctx, "session-1")
				}
				select {
				case appendError := <-done:
					if appendError != nil || readError != nil {
						t.Fatalf("concurrent read = %v, append = %v", readError, appendError)
					}
					return
				default:
				}
			}
			if appendError := <-done; appendError != nil {
				t.Errorf("append: %v", appendError)
			}
			t.Fatalf("live %s query rejected valid appended events: %v", reader, readError)
		})
	}
}
