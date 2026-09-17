package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const defaultProcessSnapshotInterval = 5 * time.Minute

type ProcessInfo struct {
	PID             uint32    `json:"pid"`
	ParentPID       uint32    `json:"parentPid"`
	CreatedUTC      time.Time `json:"createdUtc,omitempty"`
	ImageName       string    `json:"imageName"`
	ImagePath       string    `json:"imagePath,omitempty"`
	ImageSHA256     string    `json:"imageSha256,omitempty"`
	Publisher       string    `json:"publisher,omitempty"`
	SignatureStatus string    `json:"signatureStatus,omitempty"`
	UserSID         string    `json:"userSid,omitempty"`
	Username        string    `json:"username,omitempty"`
	ParentChain     string    `json:"parentChain,omitempty"`
}

func (process ProcessInfo) Key() string {
	if process.CreatedUTC.IsZero() {
		return fmt.Sprintf("%d:unknown:%s", process.PID, process.ImageName)
	}
	return fmt.Sprintf("%d:%d", process.PID, process.CreatedUTC.UnixNano())
}

type ProcessSnapshotEntry struct {
	PID         uint32    `json:"pid"`
	ParentPID   uint32    `json:"parentPid"`
	CreatedUTC  time.Time `json:"createdUtc,omitempty"`
	ImageName   string    `json:"imageName"`
	ImagePath   string    `json:"imagePath,omitempty"`
	ParentChain string    `json:"parentChain,omitempty"`
	Reasons     []string  `json:"reasons"`
}

type ProcessSnapshotPayload struct {
	IntervalSeconds      int64                  `json:"intervalSeconds"`
	ObservedProcessCount int                    `json:"observedProcessCount"`
	RelevantProcessCount int                    `json:"relevantProcessCount"`
	Processes            []ProcessSnapshotEntry `json:"processes"`
}

type processSnapshot func() ([]ProcessInfo, error)

type ProcessCollector struct {
	interval time.Duration
	snapshot processSnapshot
	now      func() time.Time
}

// The legacy switches remain in the transport model so an older saved policy
// can still be decoded. Process collection now always writes one filtered
// snapshot and does not emit lifecycle, installer, or image metadata events.
type ProcessCollectorOptions struct {
	Interval             time.Duration
	RecordProcessStart   bool
	RecordProcessStop    bool
	DetectInstallers     bool
	CaptureImageMetadata bool
}

func NewProcessCollector(interval time.Duration) (*ProcessCollector, error) {
	return newProcessCollector(interval, snapshotWindowsProcesses, time.Now)
}

func NewProcessCollectorWithOptions(options ProcessCollectorOptions) (*ProcessCollector, error) {
	return newProcessCollectorWithOptions(options, snapshotWindowsProcesses, time.Now)
}

func newProcessCollector(interval time.Duration, snapshot processSnapshot, now func() time.Time) (*ProcessCollector, error) {
	return newProcessCollectorWithOptions(ProcessCollectorOptions{Interval: interval}, snapshot, now)
}

func newProcessCollectorWithOptions(options ProcessCollectorOptions, snapshot processSnapshot, now func() time.Time) (*ProcessCollector, error) {
	interval := options.Interval
	if interval == 0 {
		interval = defaultProcessSnapshotInterval
	}
	if interval < 0 {
		return nil, errors.New("process snapshot interval must be positive")
	}
	if snapshot == nil || now == nil {
		return nil, errors.New("process collector dependency is required")
	}
	return &ProcessCollector{interval: interval, snapshot: snapshot, now: now}, nil
}

func (*ProcessCollector) Name() string { return "windows_process_snapshot" }

func (collector *ProcessCollector) Run(ctx context.Context, sink Sink) error {
	var previous []ProcessInfo
	hadGap := false
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()

	for {
		current, err := collector.snapshot()
		if err != nil {
			hadGap = true
			if emitErr := collector.emitSnapshotHealth(ctx, sink, "process_snapshot_unavailable", err); emitErr != nil {
				return emitErr
			}
		} else {
			if hadGap {
				if emitErr := collector.emitSnapshotHealth(ctx, sink, "process_snapshot_reconciled", nil); emitErr != nil {
					return emitErr
				}
				hadGap = false
			}
			if err := collector.emitRelevantSnapshot(ctx, sink, previous, current); err != nil {
				return err
			}
			previous = append(previous[:0], current...)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (collector *ProcessCollector) emitRelevantSnapshot(ctx context.Context, sink Sink, previous, current []ProcessInfo) error {
	entries := relevantProcessSnapshot(previous, current)
	payload, err := json.Marshal(ProcessSnapshotPayload{
		IntervalSeconds:      int64(collector.interval / time.Second),
		ObservedProcessCount: len(current),
		RelevantProcessCount: len(entries),
		Processes:            entries,
	})
	if err != nil {
		return fmt.Errorf("encode process relevance snapshot: %w", err)
	}
	if err := sink.Emit(ctx, Observation{
		Category: domain.EventCategoryProcess, Action: "process_relevance_snapshot", Severity: domain.EventSeverityLow,
		ObservedUTC: collector.now().UTC(), Source: collector.Name(), Confidence: domain.EventConfidenceSnapshotDiff,
		Payload: payload,
	}); err != nil && !errors.Is(err, ErrObservationQueueFull) {
		return fmt.Errorf("emit process relevance snapshot: %w", err)
	}
	return nil
}

func (collector *ProcessCollector) emitSnapshotHealth(ctx context.Context, sink Sink, action string, cause error) error {
	payload := []byte(`{"status":"reconciled"}`)
	severity := domain.EventSeverityLow
	if cause != nil {
		payload, _ = json.Marshal(map[string]string{"error": cause.Error()})
		severity = domain.EventSeverityHigh
	}
	return sink.Emit(ctx, Observation{
		Category: domain.EventCategoryHealth, Action: action, Severity: severity,
		ObservedUTC: collector.now().UTC(), Source: collector.Name(), Confidence: domain.EventConfidenceDirect, Payload: payload,
	})
}

func relevantProcessSnapshot(previous, current []ProcessInfo) []ProcessSnapshotEntry {
	previousKeys := make(map[string]struct{}, len(previous))
	previousPathsByName := make(map[string]map[string]struct{})
	for _, process := range previous {
		previousKeys[process.Key()] = struct{}{}
		name := normalizedProcessName(process)
		path := normalizedProcessPath(process.ImagePath)
		if name == "" || path == "" {
			continue
		}
		if previousPathsByName[name] == nil {
			previousPathsByName[name] = make(map[string]struct{})
		}
		previousPathsByName[name][path] = struct{}{}
	}

	entries := make([]ProcessSnapshotEntry, 0)
	for _, process := range current {
		reasons := processRelevanceReasons(process, previousKeys, previousPathsByName)
		if len(reasons) == 0 {
			continue
		}
		entries = append(entries, ProcessSnapshotEntry{
			PID: process.PID, ParentPID: process.ParentPID, CreatedUTC: process.CreatedUTC,
			ImageName: process.ImageName, ImagePath: process.ImagePath, ParentChain: process.ParentChain,
			Reasons: reasons,
		})
	}
	sort.Slice(entries, func(left, right int) bool {
		leftName := strings.ToLower(entries[left].ImageName)
		rightName := strings.ToLower(entries[right].ImageName)
		if leftName == rightName {
			return entries[left].PID < entries[right].PID
		}
		return leftName < rightName
	})
	return entries
}

func processRelevanceReasons(process ProcessInfo, previousKeys map[string]struct{}, previousPathsByName map[string]map[string]struct{}) []string {
	name := normalizedProcessName(process)
	path := normalizedProcessPath(process.ImagePath)
	routine := isRoutineTrustedProcess(process)
	reasons := make([]string, 0, 4)
	if (path == "" || strings.TrimSpace(process.UserSID) == "") && !knownSystemProcessName(name) {
		reasons = append(reasons, "identity_or_path_unavailable")
	}
	if path != "" && name != "" && strings.ToLower(filepath.Base(path)) != name {
		reasons = append(reasons, "name_path_mismatch")
	}
	if isUserControlledProcessPath(path) {
		reasons = append(reasons, "user_controlled_path")
	}
	if hasUnusualParentChain(process) {
		reasons = append(reasons, "unusual_parent_chain")
	}
	if priorPaths := previousPathsByName[name]; name != "" && path != "" && len(priorPaths) > 0 {
		if _, seen := priorPaths[path]; !seen {
			reasons = append(reasons, "same_name_new_path")
		}
	}
	if _, seen := previousKeys[process.Key()]; !seen && !routine {
		reasons = append(reasons, "new_non_system_process")
	}
	return uniqueStrings(reasons)
}

func isRoutineTrustedProcess(process ProcessInfo) bool {
	name := normalizedProcessName(process)
	path := normalizedProcessPath(process.ImagePath)
	if knownSystemProcessName(name) && (path == "" || strings.Contains(path, `\windows\`)) {
		return true
	}
	if !strings.Contains(path, `\windows\`) && !strings.Contains(path, `\program files\`) &&
		!strings.Contains(path, `\program files (x86)\`) {
		return false
	}
	// Authenticity is used only to decide whether a routine item can be omitted.
	// The returned publisher, hash, and signature status never enter the payload.
	return processImageSignatureTrusted(process.ImagePath)
}

func knownSystemProcessName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "system", "registry", "idle", "smss.exe", "csrss.exe", "wininit.exe", "services.exe",
		"lsass.exe", "svchost.exe", "fontdrvhost.exe", "dwm.exe", "winlogon.exe", "explorer.exe",
		"sihost.exe", "taskhostw.exe", "runtimebroker.exe", "searchhost.exe", "startmenuexperiencehost.exe":
		return true
	default:
		return false
	}
}

func isUserControlledProcessPath(path string) bool {
	return strings.Contains(path, `\users\`) || strings.Contains(path, `\appdata\`) ||
		strings.Contains(path, `\temp\`) || strings.Contains(path, `\downloads\`) ||
		strings.Contains(path, `\desktop\`) || strings.Contains(path, `\documents\`)
}

func hasUnusualParentChain(process ProcessInfo) bool {
	name := normalizedProcessName(process)
	if name != "cmd.exe" && name != "powershell.exe" && name != "pwsh.exe" &&
		name != "wscript.exe" && name != "cscript.exe" && name != "mshta.exe" && name != "rundll32.exe" {
		return false
	}
	chain := strings.ToLower(process.ParentChain)
	for _, parent := range []string{"winword.exe", "excel.exe", "powerpnt.exe", "outlook.exe", "acrord32.exe", "chrome.exe", "msedge.exe", "firefox.exe"} {
		if strings.Contains(chain, parent) {
			return true
		}
	}
	return false
}

func normalizedProcessName(process ProcessInfo) string {
	name := strings.ToLower(strings.TrimSpace(process.ImageName))
	if name == "" {
		name = strings.ToLower(filepath.Base(process.ImagePath))
	}
	return name
}

func normalizedProcessPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(path))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
