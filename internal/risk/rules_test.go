package risk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestDefaultRulesDetectExpectedActivity(t *testing.T) {
	events := []Event{
		ruleEvent(1, domain.EventCategoryProcess, "process_started", `C:\Users\Raymond\Downloads\tool.exe`, "windows_process_snapshot"),
		ruleEvent(2, domain.EventCategorySystem, "registry_value_modified", `SOFTWARE\Microsoft\Windows\CurrentVersion\Run\Tool`, "windows_registry:machine_startup_64"),
		ruleEvent(3, domain.EventCategorySoftware, "registry_value_added", `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Tool\DisplayName`, "windows_registry:machine_software_64"),
		ruleEvent(4, domain.EventCategoryDevice, "device_connected", `USB\VID_1234`, "windows_device_snapshot"),
		ruleEvent(5, domain.EventCategoryHealth, "observation_queue_overflow", "", "event_writer"),
		ruleEvent(6, domain.EventCategorySystem, "system_clock_jump_detected", "system-clock", "monotonic_clock_comparison"),
		ruleEvent(7, domain.EventCategoryFile, "file_modified", `C:\Evidence\report.docx`, "windows_directory_changes"),
	}
	evaluator, err := NewEvaluator(DefaultRules()...)
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("unexpected rule failures: %+v", result.Failures)
	}
	if len(result.Findings) != 7 {
		t.Fatalf("finding count = %d, want 7: %+v", len(result.Findings), result.Findings)
	}
	if len(result.RuleVersions) != 7 {
		t.Fatalf("rule version count = %d, want 7: %+v", len(result.RuleVersions), result.RuleVersions)
	}
	for _, version := range result.RuleVersions {
		if version.Version != defaultRuleVersion {
			t.Fatalf("rule version = %#v, want %q", version, defaultRuleVersion)
		}
	}
	wantRules := map[string]bool{
		sensitiveProcessRuleID: false, startupChangeRuleID: false, softwareChangeRuleID: false,
		deviceConnectionRuleID: false, collectionGapRuleID: false,
		securityStateRuleID: false,
		fileActivityRuleID:  false,
	}
	for _, finding := range result.Findings {
		wantRules[finding.RuleID] = true
	}
	for ruleID, found := range wantRules {
		if !found {
			t.Errorf("rule %s did not produce a finding", ruleID)
		}
	}
}

func TestRulesForPolicyIncludesOnlyEnabledRules(t *testing.T) {
	policy := domain.RiskRulePolicy{FileActivity: true, DeviceConnection: true, SecurityState: true}
	rules := RulesForPolicy(policy)
	want := []string{deviceConnectionRuleID, securityStateRuleID, fileActivityRuleID}
	if len(rules) != len(want) {
		t.Fatalf("rule count = %d, want %d", len(rules), len(want))
	}
	for index, rule := range rules {
		if rule.ID() != want[index] {
			t.Fatalf("rule[%d] = %q, want %q", index, rule.ID(), want[index])
		}
	}
}

func TestFileActivityRuleProducesLowRiskFinding(t *testing.T) {
	events := []Event{
		ruleEvent(1, domain.EventCategoryFile, "file_modified", `C:\Evidence\report.docx`, "windows_directory_changes"),
		ruleEvent(2, domain.EventCategoryFile, "file_truncated", `C:\Evidence\report.docx`, "windows_directory_changes"),
	}
	evaluator, err := NewEvaluator(FileActivityRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Level != LevelLow || len(result.Findings[0].Evidence) != 2 {
		t.Fatalf("file activity findings = %+v", result.Findings)
	}
}

func TestCollectionGapRuleRecognizesUSNAndHashQueueGaps(t *testing.T) {
	events := []Event{
		ruleEvent(1, domain.EventCategoryHealth, "usn_journal_gap_detected", `C:\\`, "ntfs_usn_journal"),
		ruleEvent(2, domain.EventCategoryHealth, "file_hash_queue_overflow", `C:\\Evidence\\large.bin`, "windows_directory_changes"),
		ruleEvent(3, domain.EventCategoryHealth, "system_asset_snapshot_unavailable", `system-asset-snapshot`, "windows_system_asset_snapshot"),
		ruleEvent(4, domain.EventCategoryHealth, "security_log_monitor_unavailable", `Windows Security`, "windows_security_log"),
	}
	evaluator, err := NewEvaluator(CollectionGapRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil {
		t.Fatal(err)
	}
	if result.CoverageGapCount != 4 || len(result.Findings) != 4 {
		t.Fatalf("new collection gaps were not evaluated: %+v", result)
	}
}

func TestRulesGroupRepeatedEvidenceByObject(t *testing.T) {
	first := ruleEvent(1, domain.EventCategoryDevice, "device_connected", `USB\VID_1234`, "windows_device_snapshot")
	second := ruleEvent(2, domain.EventCategoryDevice, "device_reconnected", `usb\vid_1234`, "windows_device_snapshot")
	evaluator, err := NewEvaluator(DeviceConnectionRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || len(result.Findings[0].Evidence) != 2 {
		t.Fatalf("unexpected grouped finding: %+v", result.Findings)
	}
	if !result.Findings[0].FirstObservedUTC.Equal(first.AuditEvent.ObservedUTC) ||
		!result.Findings[0].LastObservedUTC.Equal(second.AuditEvent.ObservedUTC) {
		t.Fatal("grouped finding time range is incorrect")
	}
}

func TestSoftwareRuleRecognizesInventoryInstallerAndPortableActivity(t *testing.T) {
	events := []Event{
		ruleEvent(1, domain.EventCategorySoftware, "software_installed", "Example Tool", "windows_system_asset_snapshot"),
		ruleEvent(2, domain.EventCategorySoftware, "software_installer_started", `C:\Downloads\setup.exe`, "windows_process_snapshot"),
		ruleEvent(3, domain.EventCategorySoftware, "portable_program_executed", `D:\Tools\tool.exe`, "windows_process_snapshot"),
		ruleEvent(4, domain.EventCategorySoftware, "software_repaired", "Example Tool", "windows_msi_repair_log"),
	}
	evaluator, err := NewEvaluator(SoftwareChangeRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil || len(result.Findings) != 3 {
		t.Fatalf("software findings=%+v error=%v", result.Findings, err)
	}
	foundRepair := false
	for _, finding := range result.Findings {
		for _, evidence := range finding.Evidence {
			foundRepair = foundRepair || evidence.Action == "software_repaired"
		}
	}
	if !foundRepair {
		t.Fatalf("software repair evidence missing: %+v", result.Findings)
	}
}

func TestCollectionGapRuleRecognizesProcessAndSoftwareRepairMonitorGaps(t *testing.T) {
	events := []Event{
		ruleEvent(1, domain.EventCategoryHealth, "process_snapshot_unavailable", "", "windows_process_snapshot"),
		ruleEvent(2, domain.EventCategoryHealth, "software_repair_monitor_unavailable", "", "windows_msi_repair_log"),
	}
	evaluator, err := NewEvaluator(CollectionGapRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil || len(result.Findings) != 2 {
		t.Fatalf("gap findings=%+v error=%v", result.Findings, err)
	}
}

func TestCollectionGapRuleRecognizesSleepRecovery(t *testing.T) {
	event := ruleEvent(1, domain.EventCategoryHealth, "service_recovered_after_sleep", "", "windows_power_event")
	evaluator, err := NewEvaluator(CollectionGapRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil || len(result.Findings) != 1 {
		t.Fatalf("findings=%+v error=%v", result.Findings, err)
	}
}

func TestSensitiveProcessRuleIgnoresOrdinaryProtectedPath(t *testing.T) {
	event := ruleEvent(1, domain.EventCategoryProcess, "process_started", `C:\Program Files\Example\example.exe`, "windows_process_snapshot")
	evaluator, err := NewEvaluator(SensitiveProcessRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("ordinary process produced finding: %+v", result.Findings)
	}
}

func TestStartupRuleRecognizesBaselineStartupItemChange(t *testing.T) {
	event := ruleEvent(1, domain.EventCategorySoftware, "startup_item_added", "Example", "windows_system_asset_snapshot")
	evaluator, err := NewEvaluator(StartupChangeRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil || len(result.Findings) != 1 {
		t.Fatalf("findings=%+v error=%v", result.Findings, err)
	}
}

func TestSecurityStateRuleRecognizesProxyConfigurationChange(t *testing.T) {
	event := ruleEvent(1, domain.EventCategorySystem, "proxy_configuration_changed", "local-system", "windows_system_asset_snapshot")
	evaluator, err := NewEvaluator(SecurityStateRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil || len(result.Findings) != 1 {
		t.Fatalf("findings=%+v error=%v", result.Findings, err)
	}
}

func TestSensitiveProcessRuleRecognizesDualUseToolFromPayload(t *testing.T) {
	event := ruleEvent(1, domain.EventCategoryProcess, "process_started", "", "windows_process_snapshot")
	event.Payload = []byte(`{"imageName":"powershell.exe"}`)
	evaluator, err := NewEvaluator(SensitiveProcessRule{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("dual-use process finding count = %d, want 1", len(result.Findings))
	}
}

func ruleEvent(sequence uint64, category domain.EventCategory, action, objectKey, source string) Event {
	return Event{AuditEvent: domain.AuditEvent{
		EventID: fmt.Sprintf("event-%d", sequence), SessionID: "session-1", Sequence: sequence,
		Category: category, Action: action, Severity: domain.EventSeverityLow,
		ObservedUTC: time.Date(2026, 8, 23, 9, 0, int(sequence), 0, time.UTC),
		ProcessKey:  fmt.Sprintf("process-%d", sequence), ObjectKey: objectKey,
		Source: source, Confidence: domain.EventConfidenceDirect,
	}}
}
