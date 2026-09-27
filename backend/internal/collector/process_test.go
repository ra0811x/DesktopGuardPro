package collector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProcessCollectorEmitsOneFilteredSnapshotWithoutLifecycleEvents(t *testing.T) {
	created := time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)
	processes := []ProcessInfo{
		{PID: 4, ImageName: "System"},
		{PID: 100, ParentPID: 4, CreatedUTC: created, ImageName: "svchost.exe", ImagePath: `C:\Windows\System32\svchost.exe`, UserSID: "S-1-5-18"},
		{PID: 200, ParentPID: 100, CreatedUTC: created, ImageName: "report.exe", ImagePath: `C:\Users\Raymond\Downloads\report.exe`, ImageSHA256: "secret-hash", Publisher: "Example", SignatureStatus: "untrusted", UserSID: "S-1-5-21-user", Username: "Raymond"},
	}
	collector, err := newProcessCollector(time.Hour, func() ([]ProcessInfo, error) {
		return processes, nil
	}, func() time.Time { return created.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &observationSink{onEmit: func(count int) { cancel() }}
	if err := collector.Run(ctx, sink); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "process_relevance_snapshot" ||
		observations[0].Category != "process" {
		t.Fatalf("process observations = %#v", observations)
	}
	var payload ProcessSnapshotPayload
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ObservedProcessCount != 3 || payload.RelevantProcessCount != 1 || len(payload.Processes) != 1 ||
		payload.Processes[0].ImageName != "report.exe" || !containsString(payload.Processes[0].Reasons, "user_controlled_path") {
		t.Fatalf("filtered process snapshot = %#v", payload)
	}
	persisted := string(observations[0].Payload)
	for _, forbidden := range []string{"secret-hash", "Example", "signatureStatus", "userSid", "username"} {
		if strings.Contains(persisted, forbidden) {
			t.Fatalf("snapshot persisted forbidden process metadata %q: %s", forbidden, persisted)
		}
	}
}

func TestRelevantProcessSnapshotClassifiesAbnormalProcesses(t *testing.T) {
	created := time.Date(2026, 9, 17, 2, 0, 0, 0, time.UTC)
	previous := []ProcessInfo{
		{PID: 10, CreatedUTC: created, ImageName: "agent.exe", ImagePath: `C:\Program Files\Agent\agent.exe`, UserSID: "user"},
	}
	current := []ProcessInfo{
		{PID: 11, CreatedUTC: created.Add(time.Minute), ImageName: "agent.exe", ImagePath: `D:\Portable\agent.exe`, UserSID: "user"},
		{PID: 12, CreatedUTC: created.Add(time.Minute), ImageName: "document.exe", ImagePath: `C:\Users\Raymond\AppData\Local\Temp\invoice.exe`, UserSID: "user"},
		{PID: 13, CreatedUTC: created.Add(time.Minute), ImageName: "powershell.exe", ImagePath: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, ParentChain: "99:WINWORD.EXE -> 4:System", UserSID: "user"},
		{PID: 14, CreatedUTC: created.Add(time.Minute), ImageName: "mystery.exe"},
	}

	entries := relevantProcessSnapshot(previous, current)
	if len(entries) != 4 {
		t.Fatalf("relevant process entries = %#v", entries)
	}
	byName := make(map[string]ProcessSnapshotEntry)
	for _, entry := range entries {
		byName[entry.ImageName] = entry
	}
	if !containsString(byName["agent.exe"].Reasons, "same_name_new_path") {
		t.Fatalf("agent reasons = %#v", byName["agent.exe"].Reasons)
	}
	if !containsString(byName["document.exe"].Reasons, "name_path_mismatch") ||
		!containsString(byName["document.exe"].Reasons, "user_controlled_path") {
		t.Fatalf("document reasons = %#v", byName["document.exe"].Reasons)
	}
	if !containsString(byName["powershell.exe"].Reasons, "unusual_parent_chain") {
		t.Fatalf("powershell reasons = %#v", byName["powershell.exe"].Reasons)
	}
	if !containsString(byName["mystery.exe"].Reasons, "identity_or_path_unavailable") {
		t.Fatalf("mystery reasons = %#v", byName["mystery.exe"].Reasons)
	}
}

func TestProcessCollectorRecoversAfterSnapshotGap(t *testing.T) {
	created := time.Date(2026, 9, 17, 3, 0, 0, 0, time.UTC)
	call := 0
	collector, err := newProcessCollector(time.Millisecond, func() ([]ProcessInfo, error) {
		call++
		if call == 1 {
			return nil, errors.New("snapshot unavailable")
		}
		return []ProcessInfo{{PID: 20, CreatedUTC: created, ImageName: "new.exe", ImagePath: `C:\Users\Raymond\new.exe`, UserSID: "user"}}, nil
	}, func() time.Time { return created.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sink := &observationSink{onEmit: func(count int) {
		if count == 3 {
			cancel()
		}
	}}
	if err := collector.Run(ctx, sink); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	actions := make([]string, 0, 3)
	for _, observation := range sink.snapshot() {
		actions = append(actions, observation.Action)
	}
	if strings.Join(actions, ",") != "process_snapshot_unavailable,process_snapshot_reconciled,process_relevance_snapshot" {
		t.Fatalf("recovery actions = %#v", actions)
	}
}

func TestDefaultProcessSnapshotIntervalIsFiveMinutes(t *testing.T) {
	collector, err := newProcessCollectorWithOptions(ProcessCollectorOptions{}, func() ([]ProcessInfo, error) { return nil, nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if collector.interval != 5*time.Minute {
		t.Fatalf("default interval = %s, want 5m", collector.interval)
	}
}

func TestWindowsProcessSnapshotReturnsCurrentProcess(t *testing.T) {
	processes, err := snapshotWindowsProcesses()
	if err != nil {
		t.Fatalf("snapshotWindowsProcesses() error = %v", err)
	}
	if len(processes) == 0 {
		t.Fatal("snapshotWindowsProcesses() returned no processes")
	}
}

func TestPopulateParentChains(t *testing.T) {
	processes := []ProcessInfo{
		{PID: 1, ImageName: "services.exe"},
		{PID: 10, ParentPID: 1, ImageName: "cmd.exe"},
		{PID: 20, ParentPID: 10, ImageName: "tool.exe"},
	}
	populateParentChains(processes)
	if processes[2].ParentChain != "10:cmd.exe -> 1:services.exe" {
		t.Fatalf("parent chain = %q", processes[2].ParentChain)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type observationSink struct {
	mutex        sync.Mutex
	observations []Observation
	onEmit       func(int)
}

func (sink *observationSink) Emit(_ context.Context, observation Observation) error {
	sink.mutex.Lock()
	sink.observations = append(sink.observations, observation)
	count := len(sink.observations)
	sink.mutex.Unlock()
	if sink.onEmit != nil {
		sink.onEmit(count)
	}
	return nil
}

func (sink *observationSink) snapshot() []Observation {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	return append([]Observation(nil), sink.observations...)
}
