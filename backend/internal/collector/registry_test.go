package collector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/domain"

	"golang.org/x/sys/windows/registry"
)

func TestRegistryCollectorReconcilesSilentChangesAcrossSubscriptionGaps(t *testing.T) {
	for _, duringEmit := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial-snapshot-gap", true: "blocked-emission-gap"}[duringEmit], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			reads, waits := 0, 0
			value := "initial"
			snapshot := func() (map[string]RegistryValue, error) {
				reads++
				if reads == 1 {
					return map[string]RegistryValue{}, nil
				}
				return map[string]RegistryValue{"key": {KeyPath: "isolated", Data: []byte(value)}}, nil
			}
			wait := func(ctx context.Context) error {
				waits++
				if duringEmit && waits == 1 {
					return nil
				}
				<-ctx.Done()
				return ctx.Err()
			}
			collector, err := newRegistryCollector("gap-test", domain.EventCategorySystem, domain.EventSeverityLow, snapshot, wait, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			sink := &observationSink{onEmit: func(count int) {
				if duringEmit && count == 1 {
					value = "changed-while-emitting"
					return
				}
				cancel()
			}}
			_ = collector.Run(ctx, sink)
			want := 1
			if duringEmit {
				want = 2
			}
			if got := len(sink.snapshot()); got != want {
				t.Fatalf("observed %d changes, want %d", got, want)
			}
		})
	}
}

func TestDiffRegistryValues(t *testing.T) {
	t.Parallel()

	previous := map[string]RegistryValue{
		"run\x00removed": {KeyPath: `HKLM\Run`, ValueName: "removed", ValueType: 1, Data: []byte("old")},
		"run\x00changed": {KeyPath: `HKLM\Run`, ValueName: "changed", ValueType: 1, Data: []byte("old")},
	}
	current := map[string]RegistryValue{
		"run\x00changed": {KeyPath: `HKLM\Run`, ValueName: "changed", ValueType: 1, Data: []byte("new")},
		"run\x00added":   {KeyPath: `HKLM\Run`, ValueName: "added", ValueType: 1, Data: []byte("value")},
	}
	changes := diffRegistryValues(previous, current)
	if len(changes) != 3 {
		t.Fatalf("change count = %d, want 3", len(changes))
	}
	actions := map[string]bool{}
	for _, change := range changes {
		actions[change.action] = true
	}
	for _, action := range []string{"registry_value_added", "registry_value_modified", "registry_value_removed"} {
		if !actions[action] {
			t.Fatalf("missing action %q in %+v", action, changes)
		}
	}
}

func TestRegistryCollectorEmitsChangesAfterNotification(t *testing.T) {
	t.Parallel()

	call := 0
	snapshot := func() (map[string]RegistryValue, error) {
		call++
		if call == 1 {
			return map[string]RegistryValue{}, nil
		}
		return map[string]RegistryValue{
			"run\x00Guard": {KeyPath: `HKLM\Run`, ValueName: "Guard", ValueType: 1, Data: []byte("guard.exe")},
		}, nil
	}
	notified := false
	wait := func(ctx context.Context) error {
		if !notified {
			notified = true
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	collector, err := newRegistryCollector(
		"test_registry", domain.EventCategorySystem, domain.EventSeverityMedium,
		snapshot, wait, func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) },
	)
	if err != nil {
		t.Fatalf("newRegistryCollector() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &observationSink{onEmit: func(int) { cancel() }}
	err = collector.Run(ctx, sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want %v", err, context.Canceled)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "registry_value_added" {
		t.Fatalf("observations = %+v", observations)
	}
	var payload struct {
		After  *RegistryValue `json:"after"`
		Source string         `json:"source"`
	}
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil {
		t.Fatalf("decode registry payload: %v", err)
	}
	if payload.After == nil || payload.After.ValueName != "Guard" || string(payload.After.Data) != "guard.exe" || payload.Source != "test_registry" {
		t.Fatalf("registry payload = %+v", payload)
	}
}

func TestDiffRegistryValuesRetainsBeforeAndAfterForModifiedValue(t *testing.T) {
	changes := diffRegistryValues(
		map[string]RegistryValue{"value": {KeyPath: `HKLM\Config`, ValueName: "Mode", Data: []byte("before")}},
		map[string]RegistryValue{"value": {KeyPath: `HKLM\Config`, ValueName: "Mode", Data: []byte("after")}},
	)
	if len(changes) != 1 || changes[0].before == nil || changes[0].after == nil ||
		string(changes[0].before.Data) != "before" || string(changes[0].after.Data) != "after" {
		t.Fatalf("registry differences = %#v", changes)
	}
}

func TestWindowsRegistrySnapshotAndNotificationCancellation(t *testing.T) {
	target := RegistryTarget{
		Name:     "machine_startup_64",
		Root:     registry.LOCAL_MACHINE,
		Path:     `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`,
		View:     registry.WOW64_64KEY,
		Category: domain.EventCategorySystem,
		Severity: domain.EventSeverityMedium,
	}
	if _, err := snapshotRegistryTarget(target); err != nil {
		t.Fatalf("snapshotRegistryTarget() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRegistryChange(ctx, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForRegistryChange() error = %v, want %v", err, context.Canceled)
	}
}

func TestDefaultRegistryTargetsIncludeSecurityAndNetworkConfiguration(t *testing.T) {
	targets := defaultRegistryTargets()
	names := make(map[string]RegistryTarget, len(targets))
	for _, target := range targets {
		names[target.Name] = target
	}
	for _, name := range []string{"firewall_policy", "remote_desktop", "login_winlogon", "login_lsa", "network_tcpip"} {
		target, found := names[name]
		if !found || target.Category != domain.EventCategorySystem || !target.WatchSubtree {
			t.Fatalf("default registry target %q = %#v, found=%t", name, target, found)
		}
	}
}

func TestUserStartupRegistryTargetsIncludeRunAndRunOnce(t *testing.T) {
	targets := appendUserStartupRegistryTargets(nil, []string{".DEFAULT", "S-1-5-21-100", "S-1-5-21-100_Classes"})
	if len(targets) != 2 || !strings.Contains(targets[0].Name, "user_startup_") ||
		!strings.HasSuffix(targets[1].Path, `\RunOnce`) {
		t.Fatalf("user startup targets = %#v", targets)
	}
}

func TestMissingRegistryTargetIsAnEmptyRetryableSnapshot(t *testing.T) {
	target := RegistryTarget{Root: registry.CURRENT_USER, Path: `Software\DesktopGuardPro\Missing-For-Test`, View: registry.WOW64_64KEY}
	values, err := snapshotRegistryTarget(target)
	if err != nil || len(values) != 0 {
		t.Fatalf("missing snapshot=%#v error=%v", values, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := waitForRegistryChange(ctx, target); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing wait error=%v", err)
	}
}

func TestRegistryTargetEnabledUsesDetailedModulePolicy(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.ProcessAndSoftwareEnabled = false
	policy.SystemAndNetwork.MonitorFirewall = false
	policy.SystemAndNetwork.MonitorStartupItems = false
	if registryTargetEnabled(RegistryTarget{Name: "machine_software_64"}, policy) ||
		registryTargetEnabled(RegistryTarget{Name: "firewall_policy"}, policy) ||
		registryTargetEnabled(RegistryTarget{Name: "machine_startup_64"}, policy) {
		t.Fatal("disabled registry targets were retained")
	}
	if !registryTargetEnabled(RegistryTarget{Name: "network_tcpip"}, policy) ||
		!registryTargetEnabled(RegistryTarget{Name: "login_lsa"}, policy) {
		t.Fatal("enabled registry targets were removed")
	}
}
