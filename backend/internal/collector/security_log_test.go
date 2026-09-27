package collector

import (
	"context"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestParseSecurityLogClearEventsExtractsActor(t *testing.T) {
	const output = `<Event><System><EventRecordID>88</EventRecordID><TimeCreated SystemTime="2026-09-13T02:00:00.0000000Z" /></System><EventData><Data Name="SubjectUserSid">S-1-5-21-1</Data><Data Name="SubjectUserName">Raymond</Data><Data Name="SubjectDomainName">WORKSTATION</Data><Data Name="SubjectLogonId">0x123</Data></EventData></Event>`
	records, err := parseSecurityLogClearEvents([]byte(output))
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%#v error=%v", records, err)
	}
	if records[0].ID != "88" || records[0].UserName != "Raymond" || records[0].LogonID != "0x123" {
		t.Fatalf("record=%#v", records[0])
	}
}

func TestSecurityLogCollectorEmitsClearOnce(t *testing.T) {
	now := time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC)
	collector, err := newSecurityLogCollector(func(context.Context, time.Time) ([]SecurityLogClearRecord, error) {
		return []SecurityLogClearRecord{{ID: "88", ObservedUTC: now, UserName: "Raymond"}}, nil
	}, time.Millisecond, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	sink := &observationSink{}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	if err := collector.Run(ctx, sink); err != nil {
		t.Fatal(err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "windows_security_log_cleared" ||
		observations[0].Category != domain.EventCategorySystem {
		t.Fatalf("observations=%#v", observations)
	}
}
