package risk

import (
	"context"
	"encoding/json"
	"strings"

	"desktopguardpro/internal/domain"
)

const (
	sensitiveProcessRuleID = "process.sensitive_start"
	startupChangeRuleID    = "system.startup_change"
	softwareChangeRuleID   = "software.inventory_change"
	deviceConnectionRuleID = "device.connection"
	collectionGapRuleID    = "health.collection_gap"
	securityStateRuleID    = "system.security_state_change"
	fileActivityRuleID     = "file.activity"
	defaultRuleVersion     = "2026.09.14.1"
)

func DefaultRules() []Rule {
	return RulesForPolicy(domain.DefaultMonitoringPolicy().RiskRules)
}

func RulesForPolicy(policy domain.RiskRulePolicy) []Rule {
	rules := make([]Rule, 0, 7)
	if policy.SensitiveProcess {
		rules = append(rules, SensitiveProcessRule{})
	}
	if policy.StartupChange {
		rules = append(rules, StartupChangeRule{})
	}
	if policy.SoftwareChange {
		rules = append(rules, SoftwareChangeRule{})
	}
	if policy.DeviceConnection {
		rules = append(rules, DeviceConnectionRule{})
	}
	if policy.CollectionGap {
		rules = append(rules, CollectionGapRule{})
	}
	if policy.SecurityState {
		rules = append(rules, SecurityStateRule{})
	}
	if policy.FileActivity {
		rules = append(rules, FileActivityRule{})
	}
	return rules
}

type FileActivityRule struct{}

func (FileActivityRule) ID() string { return fileActivityRuleID }

func (FileActivityRule) Version() string { return defaultRuleVersion }

func (FileActivityRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := current.AuditEvent
		if event.Category != domain.EventCategoryFile || !isFileActivity(event.Action) {
			continue
		}
		addEvidence(findings, Finding{
			Key: normalizedObjectKey(event), Title: "Protected file activity recorded",
			Summary: "A monitored file was created, changed, truncated, renamed, deleted, read, or opened during protection.",
			Level:   LevelLow, Score: 30, Confidence: confidenceScore(event.Confidence),
			Tags: []string{"file", "activity"},
		}, event)
	}
	return findingsFromMap(findings), nil
}

func isFileActivity(action string) bool {
	switch action {
	case "file_created", "file_added", "file_modified", "file_truncated", "file_deleted", "file_removed",
		"file_renamed", "file_rename_old_name", "file_rename_new_name", "file_read_or_opened":
		return true
	default:
		return false
	}
}

type SecurityStateRule struct{}

func (SecurityStateRule) ID() string { return securityStateRuleID }

func (SecurityStateRule) Version() string { return defaultRuleVersion }

func (SecurityStateRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := current.AuditEvent
		if !isSecurityStateActivity(event) {
			continue
		}
		addEvidence(findings, Finding{
			Key: normalizedObjectKey(event), Title: "Security-relevant system state changed",
			Summary: "A time, audit, account, network, firewall, service, driver, or remote access setting changed during protection.",
			Level:   LevelHigh, Score: 82, Confidence: confidenceScore(event.Confidence),
			Tags: []string{"system", "security", "configuration"},
		}, event)
	}
	return findingsFromMap(findings), nil
}

func isSecurityStateActivity(event domain.AuditEvent) bool {
	switch event.Action {
	case "system_clock_jump_detected", "windows_security_log_cleared", "account_configuration_changed",
		"network_configuration_changed", "service_added", "service_removed", "service_changed",
		"driver_added", "driver_removed", "driver_changed", "scheduled_task_added", "scheduled_task_removed", "scheduled_task_changed",
		"time_configuration_changed", "audit_policy_changed", "firewall_configuration_changed",
		"remote_desktop_configuration_changed", "security_center_status_changed", "proxy_configuration_changed":
		return true
	}
	if !isRegistryMutation(event.Action) {
		return false
	}
	source := strings.ToLower(event.Source)
	return strings.Contains(source, "firewall") || strings.Contains(source, "remote_desktop") ||
		strings.Contains(source, "network_tcpip") || strings.Contains(source, "login_") || strings.Contains(source, "audit")
}

type SensitiveProcessRule struct{}

func (SensitiveProcessRule) ID() string { return sensitiveProcessRuleID }

func (SensitiveProcessRule) Version() string { return defaultRuleVersion }

func (SensitiveProcessRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if current.AuditEvent.Category != domain.EventCategoryProcess || current.AuditEvent.Action != "process_started" {
			continue
		}
		path, name := processIdentity(current)
		if !isSensitiveProcess(name, path) {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(current.AuditEvent.ProcessKey))
		if key == "" {
			key = strings.ToLower(path)
		}
		if key == "" {
			key = current.AuditEvent.EventID
		}
		summary := "A process associated with administrative or scripting activity started during protection."
		if isUserWritableExecutable(path) {
			summary = "An executable started from a commonly user-writable directory during protection."
		}
		addEvidence(findings, Finding{
			Key: key, Title: "Process activity requires review", Summary: summary,
			Level: LevelMedium, Score: 65, Confidence: confidenceScore(current.AuditEvent.Confidence),
			Tags: []string{"process", "review"},
		}, current.AuditEvent)
	}
	return findingsFromMap(findings), nil
}

type StartupChangeRule struct{}

func (StartupChangeRule) ID() string { return startupChangeRuleID }

func (StartupChangeRule) Version() string { return defaultRuleVersion }

func (StartupChangeRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := current.AuditEvent
		registryStartup := event.Category == domain.EventCategorySystem && strings.Contains(event.Source, "_startup_") && isRegistryMutation(event.Action)
		baselineStartup := event.Category == domain.EventCategorySoftware && strings.HasPrefix(event.Action, "startup_item_")
		if !registryStartup && !baselineStartup {
			continue
		}
		key := normalizedObjectKey(event)
		addEvidence(findings, Finding{
			Key: key, Title: "Startup configuration changed",
			Summary: "A machine or user startup registry value was added, modified, or removed during protection.",
			Level:   LevelHigh, Score: 82, Confidence: confidenceScore(event.Confidence),
			Tags: []string{"persistence", "registry", "startup"},
		}, event)
	}
	return findingsFromMap(findings), nil
}

type SoftwareChangeRule struct{}

func (SoftwareChangeRule) ID() string { return softwareChangeRuleID }

func (SoftwareChangeRule) Version() string { return defaultRuleVersion }

func (SoftwareChangeRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := current.AuditEvent
		if event.Category != domain.EventCategorySoftware || !isSoftwareActivity(event.Action) {
			continue
		}
		addEvidence(findings, Finding{
			Key: normalizedObjectKey(event), Title: "Installed software inventory changed",
			Summary: "The machine software inventory changed during protection.",
			Level:   LevelMedium, Score: 58, Confidence: confidenceScore(event.Confidence),
			Tags: []string{"software", "inventory"},
		}, event)
	}
	return findingsFromMap(findings), nil
}

func isSoftwareActivity(action string) bool {
	return isRegistryMutation(action) || action == "software_installed" || action == "software_uninstalled" ||
		action == "software_upgraded" || action == "software_repaired" || action == "software_inventory_modified" ||
		action == "software_installer_started" || action == "portable_program_executed"
}

type DeviceConnectionRule struct{}

func (DeviceConnectionRule) ID() string { return deviceConnectionRuleID }

func (DeviceConnectionRule) Version() string { return defaultRuleVersion }

func (DeviceConnectionRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := current.AuditEvent
		if event.Category != domain.EventCategoryDevice ||
			(event.Action != "device_connected" && event.Action != "device_reconnected") {
			continue
		}
		addEvidence(findings, Finding{
			Key: normalizedObjectKey(event), Title: "External device connected",
			Summary: "A monitored external device became available during protection.",
			Level:   LevelMedium, Score: 55, Confidence: confidenceScore(event.Confidence),
			Tags: []string{"device", "connection"},
		}, event)
	}
	return findingsFromMap(findings), nil
}

type CollectionGapRule struct{}

func (CollectionGapRule) ID() string { return collectionGapRuleID }

func (CollectionGapRule) Version() string { return defaultRuleVersion }

func (CollectionGapRule) Evaluate(ctx context.Context, events []Event) ([]Finding, error) {
	findings := make(map[string]*Finding)
	for _, current := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event := current.AuditEvent
		if event.Category != domain.EventCategoryHealth || !isCollectionGap(event.Action) {
			continue
		}
		key := event.Source + ":" + event.Action
		addEvidence(findings, Finding{
			Key: key, Title: "Collection coverage gap detected",
			Summary: "The audit trail reports dropped observations, a recovery snapshot, or a service interruption boundary.",
			Level:   LevelHigh, Score: 88, Confidence: confidenceScore(event.Confidence),
			Tags: []string{"collection", "health", "coverage-gap"},
		}, event)
	}
	return findingsFromMap(findings), nil
}

type processPayload struct {
	ImageName string `json:"imageName"`
	ImagePath string `json:"imagePath"`
}

func processIdentity(event Event) (string, string) {
	path := strings.TrimSpace(event.AuditEvent.ObjectKey)
	payload := processPayload{}
	_ = json.Unmarshal(event.Payload, &payload)
	if path == "" {
		path = strings.TrimSpace(payload.ImagePath)
	}
	name := strings.ToLower(strings.TrimSpace(payload.ImageName))
	if name == "" {
		normalized := strings.ReplaceAll(path, "/", "\\")
		if separator := strings.LastIndex(normalized, "\\"); separator >= 0 {
			name = strings.ToLower(normalized[separator+1:])
		} else {
			name = strings.ToLower(normalized)
		}
	}
	return path, name
}

func isSensitiveProcess(name, path string) bool {
	sensitiveNames := map[string]struct{}{
		"cmd.exe": {}, "powershell.exe": {}, "pwsh.exe": {}, "wscript.exe": {},
		"cscript.exe": {}, "mshta.exe": {}, "rundll32.exe": {}, "regsvr32.exe": {},
	}
	_, sensitiveName := sensitiveNames[strings.ToLower(name)]
	return sensitiveName || isUserWritableExecutable(path)
}

func isUserWritableExecutable(path string) bool {
	normalized := "\\" + strings.Trim(strings.ToLower(strings.ReplaceAll(path, "/", "\\")), "\\") + "\\"
	return strings.Contains(normalized, "\\appdata\\local\\temp\\") ||
		strings.Contains(normalized, "\\downloads\\") ||
		strings.Contains(normalized, "\\desktop\\")
}

func isRegistryMutation(action string) bool {
	return action == "registry_value_added" || action == "registry_value_modified" || action == "registry_value_removed"
}

func isCollectionGap(action string) bool {
	return action == "observation_queue_overflow" || action == "directory_snapshot_required" ||
		action == "service_recovered_after_interruption" || action == "service_recovered_after_sleep" || action == "usn_journal_gap_detected" ||
		action == "file_hash_queue_overflow" || action == "strict_read_audit_unavailable" ||
		action == "system_asset_snapshot_unavailable" || action == "security_log_monitor_unavailable" ||
		action == "process_snapshot_unavailable" || action == "software_repair_monitor_unavailable"
}

func normalizedObjectKey(event domain.AuditEvent) string {
	key := strings.ToLower(strings.TrimSpace(event.ObjectKey))
	if key == "" {
		return event.EventID
	}
	return key
}

func addEvidence(findings map[string]*Finding, template Finding, event domain.AuditEvent) {
	finding, exists := findings[template.Key]
	if !exists {
		template.FirstObservedUTC = event.ObservedUTC
		template.LastObservedUTC = event.ObservedUTC
		findings[template.Key] = &template
		finding = &template
	}
	if event.ObservedUTC.Before(finding.FirstObservedUTC) {
		finding.FirstObservedUTC = event.ObservedUTC
	}
	if event.ObservedUTC.After(finding.LastObservedUTC) {
		finding.LastObservedUTC = event.ObservedUTC
	}
	if score := confidenceScore(event.Confidence); score < finding.Confidence {
		finding.Confidence = score
	}
	finding.Evidence = append(finding.Evidence, EvidenceFromEvent(event))
}

func findingsFromMap(indexed map[string]*Finding) []Finding {
	findings := make([]Finding, 0, len(indexed))
	for _, finding := range indexed {
		findings = append(findings, *finding)
	}
	return findings
}

func confidenceScore(confidence domain.EventConfidence) float64 {
	switch confidence {
	case domain.EventConfidenceDirect:
		return 0.95
	case domain.EventConfidenceCorrelated:
		return 0.85
	case domain.EventConfidenceSnapshotDiff:
		return 0.75
	default:
		return 0.5
	}
}
