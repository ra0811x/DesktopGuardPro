package collector

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestParseSecurityObjectAccessEventsExtractsReadAttribution(t *testing.T) {
	const output = `<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event"><System><EventID>4663</EventID><EventRecordID>42</EventRecordID><TimeCreated SystemTime="2026-09-10T08:00:00.0000000Z" /></System><EventData><Data Name="SubjectUserSid">S-1-5-21-100</Data><Data Name="SubjectUserName">Raymond</Data><Data Name="SubjectDomainName">WORKSTATION</Data><Data Name="SubjectLogonId">0x123</Data><Data Name="ObjectName">C:\Evidence\report.docx</Data><Data Name="ProcessName">C:\Tools\editor.exe</Data><Data Name="ProcessId">0x1a4</Data><Data Name="AccessMask">0x1</Data></EventData></Event>`
	records, err := parseSecurityObjectAccessEvents([]byte(output))
	if err != nil {
		t.Fatalf("parseSecurityObjectAccessEvents() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	record := records[0]
	if record.ID != "42" || record.ObjectPath != `C:\Evidence\report.docx` ||
		record.ProcessPath != `C:\Tools\editor.exe` || record.ProcessID != 420 || record.AccessMask != "0x1" ||
		record.UserSID != "S-1-5-21-100" || record.UserName != "Raymond" || record.DomainName != "WORKSTATION" || record.LogonID != "0x123" {
		t.Fatalf("record = %#v", record)
	}
}

func TestStrictReadAuditCollectorRecordsUnavailableAuditSource(t *testing.T) {
	queryFailure := errors.New("access denied")
	collector, err := newStrictReadAuditCollector(
		[]domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}},
		nil,
		func(context.Context, time.Time) ([]SecurityObjectAccessRecord, error) { return nil, queryFailure },
		time.Hour,
		time.Now,
	)
	if err != nil {
		t.Fatalf("newStrictReadAuditCollector() error = %v", err)
	}
	sink := &observationSink{}
	if err := collector.Run(context.Background(), sink); !errors.Is(err, queryFailure) {
		t.Fatalf("Run() error = %v, want %v", err, queryFailure)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "strict_read_audit_unavailable" {
		t.Fatalf("observations = %#v", observations)
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil || payload.Reason != "security_log_access_denied" {
		t.Fatalf("unavailable payload=%s error=%v", observations[0].Payload, err)
	}
}

func TestStrictReadAuditCollectorPreparesAndRestoresTargetAuditing(t *testing.T) {
	prepared := false
	restored := false
	ctx, cancel := context.WithCancel(context.Background())
	collector, err := newStrictReadAuditCollector(
		[]domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}},
		nil,
		func(context.Context, time.Time) ([]SecurityObjectAccessRecord, error) {
			cancel()
			return nil, nil
		},
		time.Hour,
		time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	collector.prepare = func(context.Context, []domain.MonitoringTarget) (func() error, error) {
		prepared = true
		return func() error {
			restored = true
			return nil
		}, nil
	}
	if err := collector.Run(ctx, &observationSink{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !prepared || !restored {
		t.Fatalf("prepared=%t restored=%t", prepared, restored)
	}
}

func TestFileSystemAuditPolicyEnabledRejectsDisabledPolicy(t *testing.T) {
	if err := fileSystemAuditPolicyEnabled("File System  No Auditing"); err == nil {
		t.Fatal("disabled File System audit policy was accepted")
	}
	if err := fileSystemAuditPolicyEnabled("File System  Success and Failure"); err != nil {
		t.Fatalf("enabled File System audit policy rejected: %v", err)
	}
}

func TestPrepareFileSystemAuditPolicyEnablesAndRestoresDisabledPolicy(t *testing.T) {
	originalQuery := queryStrictAuditPolicy
	originalSet := setFileSystemAuditPolicy
	t.Cleanup(func() {
		queryStrictAuditPolicy = originalQuery
		setFileSystemAuditPolicy = originalSet
	})
	queryStrictAuditPolicy = func(context.Context) ([]byte, error) {
		return []byte("File System  No Auditing"), nil
	}
	states := make([]fileSystemAuditPolicyState, 0, 2)
	setFileSystemAuditPolicy = func(_ context.Context, state fileSystemAuditPolicyState) error {
		states = append(states, state)
		return nil
	}

	restore, err := prepareFileSystemAuditPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	want := []fileSystemAuditPolicyState{{Success: true}, {}}
	if len(states) != len(want) || states[0] != want[0] || states[1] != want[1] {
		t.Fatalf("policy states = %+v, want %+v", states, want)
	}
}

func TestStrictReadAuditTreatsRemovableVolumeAsRecursive(t *testing.T) {
	targets := []domain.MonitoringTarget{{Path: `E:\`, Kind: domain.MonitoringTargetKindRemovableVolume}}
	if !strictReadAuditTargetMatches(targets, `E:\Evidence\Nested\report.docx`) {
		t.Fatal("nested removable-volume path was excluded from strict auditing")
	}
}

func TestStrictReadAuditCollectorFiltersTargetsAndProcessExclusions(t *testing.T) {
	now := time.Date(2026, time.September, 10, 8, 0, 0, 0, time.UTC)
	collector, err := newStrictReadAuditCollector(
		[]domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}},
		[]domain.MonitoringExclusion{{Kind: domain.MonitoringExclusionKindProcess, Pattern: "backup.exe"}},
		func(context.Context, time.Time) ([]SecurityObjectAccessRecord, error) {
			return []SecurityObjectAccessRecord{
				{ID: "outside", ObservedUTC: now, ObjectPath: `D:\Other\report.docx`, ProcessPath: `C:\Tools\editor.exe`, ProcessID: 1, AccessMask: "0x1"},
				{ID: "excluded", ObservedUTC: now, ObjectPath: `C:\Evidence\copy.docx`, ProcessPath: `C:\Tools\backup.exe`, ProcessID: 2, AccessMask: "0x1"},
				{ID: "write-only", ObservedUTC: now, ObjectPath: `C:\Evidence\draft.docx`, ProcessPath: `C:\Tools\editor.exe`, ProcessID: 4, AccessMask: "0x2"},
				{ID: "included", ObservedUTC: now, ObjectPath: `C:\Evidence\report.docx`, ProcessPath: `C:\Tools\editor.exe`, ProcessID: 3, AccessMask: "0x1", UserSID: "S-1-5-21-100", UserName: "Raymond", DomainName: "WORKSTATION", LogonID: "0x123"},
			}, nil
		},
		time.Hour,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("newStrictReadAuditCollector() error = %v", err)
	}
	sink := &observationSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx, sink) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "file_read_or_opened" || observations[0].ProcessKey != "3" {
		t.Fatalf("observations = %#v", observations)
	}
	var payload struct {
		ProcessPath string `json:"processPath"`
		ProcessID   uint32 `json:"processId"`
		UserName    string `json:"userName"`
		DomainName  string `json:"domainName"`
		LogonID     string `json:"logonId"`
	}
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil || payload.ProcessPath != `C:\Tools\editor.exe` || payload.ProcessID != 3 ||
		payload.UserName != "Raymond" || payload.DomainName != "WORKSTATION" || payload.LogonID != "0x123" {
		t.Fatalf("strict audit payload=%s error=%v", observations[0].Payload, err)
	}
	wantUserHash := sha256.Sum256([]byte("S-1-5-21-100"))
	if string(observations[0].UserSIDHash) != string(wantUserHash[:]) {
		t.Fatalf("user SID hash = %x, want %x", observations[0].UserSIDHash, wantUserHash)
	}
}
