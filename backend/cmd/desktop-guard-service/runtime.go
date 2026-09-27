package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"desktopguardpro/internal/collector"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/installpolicy"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
	platformwindows "desktopguardpro/internal/windows"

	"golang.org/x/sys/windows"
)

const applicationDataDirectoryName = "DesktopGuardPro"

const (
	automaticRetentionPeriod     = 90 * 24 * time.Hour
	automaticRetentionInterval   = 24 * time.Hour
	automaticRetentionPruneLimit = 100
)

type terminalSessionPruner interface {
	PruneTerminalSessions(context.Context, time.Time, int) ([]string, error)
}

type applicationRuntime struct {
	database       *sql.DB
	repository     *storage.Repository
	coordinator    *coreservice.Coordinator
	api            *coreservice.API
	collectors     *collector.SessionController
	powerMutex     sync.Mutex
	sleepStart     time.Time
	sleepSessionID string
}

const (
	powerEventSuspend       = 0x0004
	powerEventResumeSuspend = 0x0007
	powerEventResumeAuto    = 0x0012
)

func (runtime *applicationRuntime) handlePowerEvent(eventType uint32) error {
	now := time.Now().UTC()
	switch eventType {
	case powerEventSuspend:
		session, ok := runtime.coordinator.Current()
		if !ok || (session.State != domain.SessionStateActive && session.State != domain.SessionStateDegraded) {
			return nil
		}
		runtime.powerMutex.Lock()
		runtime.sleepStart, runtime.sleepSessionID = now, session.ID
		runtime.powerMutex.Unlock()
		return runtime.appendPowerBoundary(session.ID, "system_sleep_started", domain.EventSeverityMedium, map[string]any{"sleepStartUtc": now})
	case powerEventResumeSuspend, powerEventResumeAuto:
		runtime.powerMutex.Lock()
		start, sessionID := runtime.sleepStart, runtime.sleepSessionID
		runtime.sleepStart, runtime.sleepSessionID = time.Time{}, ""
		runtime.powerMutex.Unlock()
		if start.IsZero() || sessionID == "" {
			return nil
		}
		session, ok := runtime.coordinator.Current()
		if !ok || session.ID != sessionID {
			return nil
		}
		return runtime.appendPowerBoundary(sessionID, "service_recovered_after_sleep", domain.EventSeverityHigh, map[string]any{
			"sleepStartUtc": start, "recoveredUtc": now, "durationMillis": now.Sub(start).Milliseconds(),
		})
	}
	return nil
}

func (runtime *applicationRuntime) appendPowerBoundary(sessionID, action string, severity domain.EventSeverity, payloadValue any) error {
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	eventID, err := newRecoveryEventID()
	if err != nil {
		return err
	}
	observedUTC := time.Now().UTC()
	_, err = runtime.repository.AppendEventAutoSequence(context.Background(), domain.AuditEvent{
		EventID: eventID, SessionID: sessionID, Category: domain.EventCategoryHealth, Action: action, Severity: severity,
		ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(), Source: "windows_power_event", Confidence: domain.EventConfidenceDirect,
	}, payload)
	return err
}

func defaultDataDirectory() (string, error) {
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("resolve ProgramData directory: %w", err)
	}
	return filepath.Join(programData, applicationDataDirectoryName), nil
}

func openApplicationRuntime(ctx context.Context, dataDirectory string) (*applicationRuntime, error) {
	policy, err := installpolicy.Load(dataDirectory)
	if err != nil {
		return nil, fmt.Errorf("load install policy: %w", err)
	}
	keyStore, err := storage.NewDataKeyStore(storage.NewDPAPIKeyProtector())
	if err != nil {
		return nil, fmt.Errorf("create key store: %w", err)
	}
	masterKey, err := keyStore.LoadOrCreate(filepath.Join(dataDirectory, "storage.key"))
	if err != nil {
		return nil, fmt.Errorf("load storage key: %w", err)
	}
	defer clear(masterKey)

	payloadCipher, err := storage.NewPayloadCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create payload cipher: %w", err)
	}
	eventIntegrity, err := storage.NewEventIntegrity(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create event integrity verifier: %w", err)
	}
	database, err := storage.Open(ctx, filepath.Join(dataDirectory, "desktop-guard.db"))
	if err != nil {
		return nil, err
	}
	repository, err := storage.NewRepository(database, payloadCipher, eventIntegrity)
	if err != nil {
		_ = database.Close()
		return nil, err
	}

	coordinator := coreservice.NewCoordinator()
	recoverable, ok, err := repository.FindRecoverableSession(ctx)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("find recoverable session: %w", err)
	}
	if ok {
		if _, err := repository.ListEvents(ctx, recoverable.ID); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("verify recoverable session: %w", err)
		}
		if err := coordinator.Restore(recoverable); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("restore session: %w", err)
		}
		if recoverable.State == domain.SessionStateFinalizing {
			if err := completeRecoveredFinalization(ctx, coordinator, repository, recoverable); err != nil {
				_ = database.Close()
				return nil, fmt.Errorf("complete recovered session finalization: %w", err)
			}
		} else {
			if err := recordRecoveryBoundary(ctx, coordinator, repository, recoverable); err != nil {
				_ = database.Close()
				return nil, fmt.Errorf("record session recovery boundary: %w", err)
			}
			if err := recordUSNRecoveryBoundary(ctx, repository, recoverable); err != nil {
				_ = database.Close()
				return nil, fmt.Errorf("record USN recovery boundary: %w", err)
			}
		}
	}
	captureAssetBaseline := func(
		stage storage.AssetBaselineStage,
		preserveExisting bool,
	) collector.BaselineCapture {
		return func(captureContext context.Context, sessionID string) error {
			if preserveExisting {
				_, loadErr := repository.LoadAssetBaseline(captureContext, sessionID, stage)
				if loadErr == nil {
					return nil
				}
				if !errors.Is(loadErr, storage.ErrAssetBaselineNotFound) {
					return fmt.Errorf("load existing %s asset baseline: %w", stage, loadErr)
				}
			}
			baseline, snapshotErr := collector.SnapshotSystemAssetBaseline(captureContext)
			if snapshotErr != nil {
				return fmt.Errorf("snapshot %s asset baseline: %w", stage, snapshotErr)
			}
			session, ok := coordinator.Current()
			if !ok || session.ID != sessionID {
				return errors.New("capture asset baseline without an active session")
			}
			baseline = filterAssetBaselineForMonitoringPolicy(baseline, session.MonitoringPolicy)
			if storeErr := repository.StoreAssetBaseline(
				captureContext,
				sessionID,
				stage,
				baseline,
			); storeErr != nil {
				return fmt.Errorf("store %s asset baseline: %w", stage, storeErr)
			}
			return nil
		}
	}
	captureUSNJournalCheckpoints := func(stage storage.USNCheckpointStage) collector.BaselineCapture {
		return func(captureContext context.Context, sessionID string) error {
			targets, err := repository.LoadMonitoringTargets(captureContext)
			if err != nil {
				return fmt.Errorf("load monitoring targets for USN checkpoint: %w", err)
			}
			states, err := collector.QueryUSNJournalStates(targets)
			if err != nil {
				return err
			}
			checkpoints := make([]storage.USNJournalCheckpoint, 0, len(states))
			capturedUTC := time.Now().UTC()
			for _, state := range states {
				checkpoints = append(checkpoints, storage.USNJournalCheckpoint{
					SessionID: sessionID, Stage: stage, Volume: state.Volume,
					JournalID: state.JournalID, NextUSN: state.NextUSN, CapturedUTC: capturedUTC,
				})
			}
			return repository.StoreUSNJournalCheckpoints(captureContext, checkpoints)
		}
	}
	captureForPolicy := func(enabled func(domain.MonitoringPolicy) bool, capture collector.BaselineCapture) collector.BaselineCapture {
		return func(captureContext context.Context, sessionID string) error {
			session, ok := coordinator.Current()
			if !ok || session.ID != sessionID {
				return errors.New("capture baseline without an active session")
			}
			if !enabled(session.MonitoringPolicy) {
				return nil
			}
			return capture(captureContext, sessionID)
		}
	}
	collectorController, err := collector.NewSessionController(
		coordinator,
		repository,
		func() ([]collector.Collector, error) {
			session, ok := coordinator.Current()
			if !ok {
				return nil, errors.New("create collectors without an active session")
			}
			return collectorsForRepositoryWithMonitoringPolicy(repository, session.MonitoringPolicy)
		},
		collector.SessionControllerOptions{
			CaptureStartBaseline: captureStartBaselinesWithReview(coordinator, repository,
				namedBaselineCapture{Name: "asset_inventory", Capture: captureForPolicy(func(policy domain.MonitoringPolicy) bool {
					return policy.ProcessAndSoftwareEnabled || policy.SystemAndNetworkEnabled || policy.ExternalDevicesEnabled
				}, captureAssetBaseline(storage.AssetBaselineStageStart, true))},
				namedBaselineCapture{Name: "usn_checkpoint", Capture: captureForPolicy(func(policy domain.MonitoringPolicy) bool {
					return policy.FileActivityEnabled
				}, captureUSNJournalCheckpoints(storage.USNCheckpointStageStart))},
			),
			CaptureEndBaseline: combineBaselineCaptures(
				captureForPolicy(func(policy domain.MonitoringPolicy) bool {
					return policy.ProcessAndSoftwareEnabled || policy.SystemAndNetworkEnabled || policy.ExternalDevicesEnabled
				}, captureAssetBaseline(storage.AssetBaselineStageEnd, false)),
				captureForPolicy(func(policy domain.MonitoringPolicy) bool {
					return policy.FileActivityEnabled
				}, captureUSNJournalCheckpoints(storage.USNCheckpointStageEnd)),
			),
		},
	)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("create collector controller: %w", err)
	}

	api := coreservice.NewPersistentAuthorizedAPI(coordinator, repository, policy.OwnerUserSID)
	api.SetHealthStatusSource(func() string { return string(collectorController.HealthStatus()) })
	api.SetSessionLifecycle(collectorController)
	api.SetSystemCredentialVerifier(platformwindows.NewSystemCredentialVerifier())
	api.SetInputShieldCredentialManager(coreservice.NewInputShieldCredentialManager(repository))
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) {
		return collector.NormalizeDirectoryRoots(roots, dataDirectory)
	})
	api.SetMonitoringTargetValidator(func(targets []domain.MonitoringTarget) ([]domain.MonitoringTarget, error) {
		return collector.NormalizeMonitoringTargets(targets, dataDirectory)
	})
	agentExecutable, err := configuredAgentExecutable()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	api.SetAgentExecutable(agentExecutable)
	api.SetAgentActivityHandler(func(
		activityContext context.Context,
		client coreservice.ClientIdentity,
		report coreservice.AgentActivityReportRequest,
	) error {
		observation, observationErr := agentActivityObservation(client, report)
		if observationErr != nil {
			return observationErr
		}
		return collectorController.EmitExternalObservation(activityContext, report.SessionID, observation)
	})
	api.SetAgentInputShieldHandler(func(
		activityContext context.Context,
		client coreservice.ClientIdentity,
		report coreservice.AgentInputShieldReportRequest,
	) error {
		observation, record, observationErr := agentInputShieldObservation(client, report)
		if observationErr != nil {
			return observationErr
		}
		if !record {
			return nil
		}
		return collectorController.EmitExternalObservation(activityContext, report.SessionID, observation)
	})

	return &applicationRuntime{
		database:    database,
		repository:  repository,
		coordinator: coordinator,
		api:         api,
		collectors:  collectorController,
	}, nil
}

func filterAssetBaselineForMonitoringPolicy(
	baseline domain.AssetBaseline,
	policy domain.MonitoringPolicy,
) domain.AssetBaseline {
	assets := make([]domain.Asset, 0, len(baseline.Assets))
	for _, asset := range baseline.Assets {
		enabled := false
		switch asset.Category {
		case domain.AssetCategorySoftware:
			enabled = policy.ProcessAndSoftwareEnabled
		case domain.AssetCategoryDevice:
			enabled = policy.ExternalDevicesEnabled
		case domain.AssetCategoryNetwork, domain.AssetCategoryAccount, domain.AssetCategorySystem:
			enabled = policy.SystemAndNetworkEnabled
		}
		if enabled {
			assets = append(assets, asset)
		}
	}
	baseline.Assets = assets
	return baseline
}

type inputActivityPreferenceLoader interface {
	LoadInputActivityEnabled(context.Context) (bool, error)
}

type windowTitlePreferenceLoader interface {
	LoadWindowTitleEnabled(context.Context) (bool, error)
}

type highRiskShortcutsPreferenceLoader interface {
	LoadHighRiskShortcutsEnabled(context.Context) (bool, error)
}

func applyInputActivityPreference(
	ctx context.Context,
	loader inputActivityPreferenceLoader,
	report coreservice.AgentActivityReportRequest,
) (coreservice.AgentActivityReportRequest, error) {
	enabled, err := loader.LoadInputActivityEnabled(ctx)
	if err != nil {
		return report, fmt.Errorf("load input activity preference: %w", err)
	}
	if enabled {
		return report, nil
	}
	report.InputActivityCount = 0
	report.KeyboardActivityCount = 0
	report.MouseClickCount = 0
	report.MouseWheelCount = 0
	report.HighRiskShortcutCount = nil
	report.ActivityStartUTC = time.Time{}
	report.ActivityEndUTC = time.Time{}
	return report, nil
}

func applyHighRiskShortcutsPreference(
	ctx context.Context,
	loader highRiskShortcutsPreferenceLoader,
	report coreservice.AgentActivityReportRequest,
) (coreservice.AgentActivityReportRequest, error) {
	enabled, err := loader.LoadHighRiskShortcutsEnabled(ctx)
	if err != nil {
		return report, fmt.Errorf("load high-risk shortcuts preference: %w", err)
	}
	if !enabled {
		report.HighRiskShortcutCount = nil
	}
	return report, nil
}

func applyWindowTitlePreference(
	ctx context.Context,
	loader windowTitlePreferenceLoader,
	report coreservice.AgentActivityReportRequest,
) (coreservice.AgentActivityReportRequest, error) {
	enabled, err := loader.LoadWindowTitleEnabled(ctx)
	if err != nil {
		return report, fmt.Errorf("load window title preference: %w", err)
	}
	if !enabled {
		report.WindowTitle = ""
	}
	return report, nil
}

func combineBaselineCaptures(captures ...collector.BaselineCapture) collector.BaselineCapture {
	return func(ctx context.Context, sessionID string) error {
		for _, capture := range captures {
			if capture != nil {
				if err := capture(ctx, sessionID); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

type namedBaselineCapture struct {
	Name    string
	Capture collector.BaselineCapture
}

func captureStartBaselinesWithReview(
	coordinator *coreservice.Coordinator,
	repository *storage.Repository,
	captures ...namedBaselineCapture,
) collector.BaselineCapture {
	return func(ctx context.Context, sessionID string) error {
		result := domain.BaselineCaptureResult{}
		for _, step := range captures {
			if step.Capture == nil {
				continue
			}
			result.AttemptedItemCount++
			if err := step.Capture(ctx, sessionID); err != nil {
				result.Failures = append(result.Failures, domain.BaselineCaptureFailure{
					Item: step.Name, Reason: err.Error(),
				})
				continue
			}
			result.SucceededItemCount++
		}
		if len(result.Failures) == 0 {
			return nil
		}
		decision, err := result.StartDecision()
		if err != nil {
			return fmt.Errorf("evaluate start baseline result: %w", err)
		}
		if err := repository.StoreBaselineReview(ctx, sessionID, result); err != nil {
			return fmt.Errorf("store baseline review: %w", err)
		}
		session, ok := coordinator.Current()
		if !ok || session.ID != sessionID {
			return errors.New("baseline review session is no longer current")
		}
		if _, err := coordinator.TransitionPersisted(domain.SessionStateBaselineReview, func(next domain.Session) error {
			return repository.UpdateSession(ctx, next, time.Now().UTC())
		}); err != nil {
			return fmt.Errorf("pause session for baseline review: %w", err)
		}
		payload, err := json.Marshal(map[string]any{"decision": decision})
		if err != nil {
			return fmt.Errorf("encode baseline review event: %w", err)
		}
		eventID, err := newRecoveryEventID()
		if err != nil {
			return err
		}
		severity := domain.EventSeverityMedium
		if decision.Status == domain.BaselineCaptureStatusFailed {
			severity = domain.EventSeverityHigh
		}
		observedUTC := time.Now().UTC()
		if _, err := repository.AppendEventAutoSequence(ctx, domain.AuditEvent{
			EventID: eventID, SessionID: sessionID, Category: domain.EventCategoryHealth,
			Action: "baseline_review_required", Severity: severity, ObservedUTC: observedUTC,
			MonotonicTicks: observedUTC.UnixNano(), Source: "baseline_capture", Confidence: domain.EventConfidenceDirect,
		}, payload); err != nil {
			return fmt.Errorf("record baseline review event: %w", err)
		}
		return nil
	}
}

func collectorsForRepository(repository *storage.Repository) ([]collector.Collector, error) {
	return collectorsForRepositoryWithMonitoringLevel(repository, domain.MonitoringLevelStandard)
}

func collectorsForRepositoryWithMonitoringLevel(
	repository *storage.Repository,
	level domain.MonitoringLevel,
) ([]collector.Collector, error) {
	policy := domain.DefaultMonitoringPolicy()
	policy.StrictReadAuditEnabled = level == domain.MonitoringLevelStrict
	return collectorsForRepositoryWithMonitoringPolicy(repository, policy)
}

func collectorsForRepositoryWithMonitoringPolicy(
	repository *storage.Repository,
	policy domain.MonitoringPolicy,
) ([]collector.Collector, error) {
	targets, err := repository.LoadMonitoringTargets(context.Background())
	if err != nil {
		return nil, err
	}
	exclusions, err := repository.LoadMonitoringExclusions(context.Background())
	if err != nil {
		return nil, err
	}
	return collector.SystemCollectorsForMonitoringPolicy(targets, exclusions, policy)
}

func configuredAgentExecutable() (string, error) {
	serviceExecutable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve service executable for agent validation: %w", err)
	}
	return filepath.Join(filepath.Dir(serviceExecutable), "desktop-guard-agent.exe"), nil
}

type agentActivityPayload struct {
	WindowTitle           string            `json:"windowTitle,omitempty"`
	ProcessID             uint32            `json:"processId,omitempty"`
	ProcessImage          string            `json:"processImage,omitempty"`
	InputActivityCount    uint64            `json:"inputActivityCount,omitempty"`
	ActivityIntensity     string            `json:"activityIntensity"`
	ActivityRatePerMinute float64           `json:"activityRatePerMinute,omitempty"`
	KeyboardActivityCount uint64            `json:"keyboardActivityCount,omitempty"`
	MouseClickCount       uint64            `json:"mouseClickCount,omitempty"`
	MouseWheelCount       uint64            `json:"mouseWheelCount,omitempty"`
	HighRiskShortcutCount map[string]uint64 `json:"highRiskShortcutCount,omitempty"`
	ActivityStartUTC      *time.Time        `json:"activityStartUtc,omitempty"`
	ActivityEndUTC        *time.Time        `json:"activityEndUtc,omitempty"`
	ForegroundStartUTC    *time.Time        `json:"foregroundStartUtc,omitempty"`
	ForegroundEndUTC      *time.Time        `json:"foregroundEndUtc,omitempty"`
	ForegroundDurationMS  int64             `json:"foregroundDurationMillis,omitempty"`
}

func agentActivityObservation(
	client coreservice.ClientIdentity,
	report coreservice.AgentActivityReportRequest,
) (collector.Observation, error) {
	var activityStartUTC, activityEndUTC *time.Time
	if !report.ActivityStartUTC.IsZero() {
		start, end := report.ActivityStartUTC.UTC(), report.ActivityEndUTC.UTC()
		activityStartUTC, activityEndUTC = &start, &end
	}
	var foregroundStartUTC, foregroundEndUTC *time.Time
	if !report.ForegroundStartUTC.IsZero() {
		start, end := report.ForegroundStartUTC.UTC(), report.ForegroundEndUTC.UTC()
		foregroundStartUTC, foregroundEndUTC = &start, &end
	}
	activityIntensity, activityRate := inputActivityIntensity(report.InputActivityCount, report.ActivityStartUTC, report.ActivityEndUTC)
	payload, err := json.Marshal(agentActivityPayload{
		WindowTitle: report.WindowTitle, ProcessID: report.ProcessID,
		ProcessImage: report.ProcessImage, InputActivityCount: report.InputActivityCount,
		ActivityIntensity: activityIntensity, ActivityRatePerMinute: activityRate,
		KeyboardActivityCount: report.KeyboardActivityCount, MouseClickCount: report.MouseClickCount,
		MouseWheelCount: report.MouseWheelCount, ActivityStartUTC: activityStartUTC, ActivityEndUTC: activityEndUTC,
		HighRiskShortcutCount: report.HighRiskShortcutCount,
		ForegroundStartUTC:    foregroundStartUTC, ForegroundEndUTC: foregroundEndUTC,
		ForegroundDurationMS: report.ForegroundDurationMS,
	})
	if err != nil {
		return collector.Observation{}, fmt.Errorf("encode agent activity payload: %w", err)
	}
	var windowsSessionID *uint32
	if client.WindowsSessionID != 0 {
		value := client.WindowsSessionID
		windowsSessionID = &value
	}
	var userSIDHash []byte
	if client.UserSID != "" {
		digest := sha256.Sum256([]byte(client.UserSID))
		userSIDHash = digest[:]
	}
	return collector.Observation{
		Category: domain.EventCategorySystem, Action: "agent_activity_summary", Severity: domain.EventSeverityLow,
		ObservedUTC: report.ObservedUTC.UTC(), MonotonicTicks: report.ObservedUTC.UnixNano(),
		WindowsSessionID: windowsSessionID, UserSIDHash: userSIDHash,
		ProcessKey: strconv.FormatUint(uint64(report.ProcessID), 10), ObjectKey: "foreground-window",
		Source: "session_agent", Confidence: domain.EventConfidenceDirect, Payload: payload,
	}, nil
}

func agentInputShieldObservation(
	client coreservice.ClientIdentity,
	report coreservice.AgentInputShieldReportRequest,
) (collector.Observation, bool, error) {
	if report.Action == "heartbeat" ||
		((report.Action == "device_connected" || report.Action == "device_removed") && report.Device == nil) {
		return collector.Observation{}, false, nil
	}
	category := domain.EventCategorySystem
	severity := domain.EventSeverityLow
	objectKey := "local-input-shield"
	switch report.Action {
	case "input_blocked", "unlock_requested":
		severity = domain.EventSeverityMedium
	case "unlock_failed", "hook_degraded":
		severity = domain.EventSeverityHigh
	case "device_connected", "device_removed":
		category = domain.EventCategoryDevice
		severity = domain.EventSeverityMedium
		if report.Device != nil && report.Device.InstanceID != "" {
			objectKey = report.Device.InstanceID
		} else {
			objectKey = "input-device"
		}
	case "started", "stopped":
		category = domain.EventCategoryHealth
	}
	if report.Action == "hook_degraded" {
		category = domain.EventCategoryHealth
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return collector.Observation{}, false, fmt.Errorf("encode input shield payload: %w", err)
	}
	var windowsSessionID *uint32
	if client.WindowsSessionID != 0 {
		value := client.WindowsSessionID
		windowsSessionID = &value
	}
	var userSIDHash []byte
	if client.UserSID != "" {
		digest := sha256.Sum256([]byte(client.UserSID))
		userSIDHash = digest[:]
	}
	return collector.Observation{
		Category: category, Action: "input_shield_" + report.Action, Severity: severity,
		ObservedUTC: report.ObservedUTC.UTC(), MonotonicTicks: report.ObservedUTC.UnixNano(),
		WindowsSessionID: windowsSessionID, UserSIDHash: userSIDHash,
		ObjectKey: objectKey, Source: "session_agent_input_shield", Confidence: domain.EventConfidenceDirect,
		Payload: payload,
	}, true, nil
}

func inputActivityIntensity(count uint64, start, end time.Time) (string, float64) {
	if count == 0 {
		return "none", 0
	}
	duration := end.Sub(start)
	if start.IsZero() || end.IsZero() || duration <= 0 {
		return "unavailable", 0
	}
	rate := float64(count) / duration.Minutes()
	switch {
	case rate < 30:
		return "low", rate
	case rate < 120:
		return "medium", rate
	default:
		return "high", rate
	}
}

type recoveryBoundaryPayload struct {
	InterruptionStartUTC time.Time `json:"interruptionStartUtc"`
	RecoveredUTC         time.Time `json:"recoveredUtc"`
	DurationMS           int64     `json:"durationMillis"`
	PreviousSequence     uint64    `json:"previousSequence"`
	BoundaryBasis        string    `json:"boundaryBasis"`
}

func completeRecoveredFinalization(
	ctx context.Context,
	coordinator *coreservice.Coordinator,
	repository *storage.Repository,
	recovered domain.Session,
) error {
	if recovered.State != domain.SessionStateFinalizing {
		return nil
	}
	observedUTC := time.Now().UTC()
	_, err := coordinator.TransitionPersisted(domain.SessionStateCompleted, func(completed domain.Session) error {
		payload, err := json.Marshal(struct {
			PreviousState  domain.SessionState `json:"previousState"`
			CurrentState   domain.SessionState `json:"currentState"`
			ObservedUTC    time.Time           `json:"observedUtc"`
			RecoveryReason string              `json:"recoveryReason"`
		}{
			PreviousState: recovered.State, CurrentState: completed.State, ObservedUTC: observedUTC,
			RecoveryReason: "service_restarted_during_finalization",
		})
		if err != nil {
			return err
		}
		eventID, err := newRecoveryEventID()
		if err != nil {
			return err
		}
		_, err = repository.UpdateSessionAndAppendEvent(ctx, completed, observedUTC, domain.AuditEvent{
			EventID: eventID, SessionID: completed.ID, Category: domain.EventCategoryHealth,
			Action: "protection_ended", Severity: domain.EventSeverityLow,
			ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(),
			Source: "desktop_guard_service", Confidence: domain.EventConfidenceDirect,
		}, payload)
		return err
	})
	return err
}

func recordRecoveryBoundary(
	ctx context.Context,
	coordinator *coreservice.Coordinator,
	repository *storage.Repository,
	recovered domain.Session,
) error {
	if recovered.State != domain.SessionStateActive && recovered.State != domain.SessionStateDegraded {
		return nil
	}
	recoveredUTC := time.Now().UTC()
	if recovered.State == domain.SessionStateActive {
		var err error
		recovered, err = coordinator.TransitionPersisted(domain.SessionStateDegraded, func(session domain.Session) error {
			return repository.UpdateSession(ctx, session, recoveredUTC)
		})
		if err != nil {
			return fmt.Errorf("persist degraded recovered session: %w", err)
		}
	}
	previousSequence, interruptionStartUTC, err := repository.LastEventBoundary(ctx, recovered.ID)
	if err != nil {
		return fmt.Errorf("read previous event sequence: %w", err)
	}
	boundaryBasis := "last_observed_event"
	if interruptionStartUTC.IsZero() || interruptionStartUTC.After(recoveredUTC) {
		interruptionStartUTC = recoveredUTC
		boundaryBasis = "recovery_time_fallback"
	}
	payload, err := json.Marshal(recoveryBoundaryPayload{
		InterruptionStartUTC: interruptionStartUTC, RecoveredUTC: recoveredUTC,
		DurationMS:       recoveredUTC.Sub(interruptionStartUTC).Milliseconds(),
		PreviousSequence: previousSequence, BoundaryBasis: boundaryBasis,
	})
	if err != nil {
		return fmt.Errorf("encode recovery payload: %w", err)
	}
	eventID, err := newRecoveryEventID()
	if err != nil {
		return err
	}
	_, err = repository.AppendEvent(ctx, domain.AuditEvent{
		EventID: eventID, SessionID: recovered.ID, Sequence: previousSequence + 1,
		Category: domain.EventCategoryHealth, Action: "service_recovered_after_interruption",
		Severity: domain.EventSeverityMedium, ObservedUTC: recoveredUTC,
		MonotonicTicks: recoveredUTC.UnixNano(), Source: "desktop_guard_service",
		Confidence: domain.EventConfidenceDirect,
	}, payload)
	if err != nil {
		return fmt.Errorf("append recovery event: %w", err)
	}
	return nil
}

type usnRecoveryGap struct {
	Volume       string `json:"volume"`
	PreviousUSN  uint64 `json:"previousUsn"`
	CurrentUSN   uint64 `json:"currentUsn"`
	JournalReset bool   `json:"journalReset"`
}

func recordUSNRecoveryBoundary(ctx context.Context, repository *storage.Repository, session domain.Session) error {
	start, err := repository.LoadUSNJournalCheckpoints(ctx, session.ID, storage.USNCheckpointStageStart)
	if err != nil {
		return err
	}
	if len(start) == 0 {
		return nil
	}
	targets, err := repository.LoadMonitoringTargets(ctx)
	if err != nil {
		return err
	}
	states, err := collector.QueryUSNJournalStates(targets)
	if err != nil {
		return err
	}
	gaps := usnRecoveryGaps(start, states)
	observedUTC := time.Now().UTC()
	if len(gaps) > 0 {
		payload, err := json.Marshal(struct {
			Gaps        []usnRecoveryGap `json:"gaps"`
			ObservedUTC time.Time        `json:"observedUtc"`
		}{Gaps: gaps, ObservedUTC: observedUTC})
		if err != nil {
			return err
		}
		eventID, err := newRecoveryEventID()
		if err != nil {
			return err
		}
		if _, err = repository.AppendEventAutoSequence(ctx, domain.AuditEvent{
			EventID: eventID, SessionID: session.ID, Category: domain.EventCategoryHealth,
			Action: "usn_journal_gap_detected", Severity: domain.EventSeverityHigh,
			ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(),
			Source: "ntfs_usn_journal", Confidence: domain.EventConfidenceDirect,
		}, payload); err != nil {
			return err
		}
	}
	if err := appendUSNRecoveryChanges(ctx, repository, session.ID, start, states); err != nil {
		return err
	}
	checkpoints := make([]storage.USNJournalCheckpoint, 0, len(states))
	for _, state := range states {
		checkpoints = append(checkpoints, storage.USNJournalCheckpoint{
			SessionID: session.ID, Stage: storage.USNCheckpointStageRecovery, Volume: state.Volume,
			JournalID: state.JournalID, NextUSN: state.NextUSN, CapturedUTC: observedUTC,
		})
	}
	return repository.StoreUSNJournalCheckpoints(ctx, checkpoints)
}

func appendUSNRecoveryChanges(
	ctx context.Context,
	repository *storage.Repository,
	sessionID string,
	start []storage.USNJournalCheckpoint,
	states []collector.USNJournalState,
) error {
	targets, err := repository.LoadMonitoringTargets(ctx)
	if err != nil {
		return err
	}
	exclusions, err := repository.LoadMonitoringExclusions(ctx)
	if err != nil {
		return err
	}
	byVolume := make(map[string]collector.USNJournalState, len(states))
	for _, state := range states {
		byVolume[state.Volume] = state
	}
	for _, checkpoint := range start {
		state, found := byVolume[checkpoint.Volume]
		if !found || state.JournalID != checkpoint.JournalID || state.NextUSN <= checkpoint.NextUSN {
			continue
		}
		changes, err := collector.ReadUSNJournalChanges(ctx, checkpoint.Volume, checkpoint.JournalID, checkpoint.NextUSN, state.NextUSN)
		if err != nil {
			continue
		}
		for _, change := range changes {
			path, resolveErr := collector.ResolveUSNChangePath(change)
			if resolveErr != nil {
				payload, encodeErr := json.Marshal(map[string]any{
					"volume": change.Volume, "fileReference": change.FileReference,
					"parentReference": change.ParentReference, "usn": change.USN,
					"fileName": change.FileName, "error": resolveErr.Error(),
				})
				if encodeErr != nil {
					return encodeErr
				}
				eventID, idErr := newRecoveryEventID()
				if idErr != nil {
					return idErr
				}
				observedUTC := time.Now().UTC()
				if _, appendErr := repository.AppendEventAutoSequence(ctx, domain.AuditEvent{
					EventID: eventID, SessionID: sessionID, Category: domain.EventCategoryHealth,
					Action: "usn_change_path_unresolved", Severity: domain.EventSeverityMedium,
					ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(),
					Source: "ntfs_usn_journal", Confidence: domain.EventConfidenceDirect,
				}, payload); appendErr != nil {
					return appendErr
				}
				continue
			}
			if !usnRecoveryPathMatches(targets, path) || domain.MatchesMonitoringExclusion(exclusions, path, "") {
				continue
			}
			payload, err := json.Marshal(struct {
				Volume          string `json:"volume"`
				Path            string `json:"path"`
				FileReference   uint64 `json:"fileReference"`
				ParentReference uint64 `json:"parentReference"`
				USN             uint64 `json:"usn"`
				Reason          uint32 `json:"reason"`
				FileName        string `json:"fileName"`
			}{change.Volume, path, change.FileReference, change.ParentReference, change.USN, change.Reason, change.FileName})
			if err != nil {
				return err
			}
			eventID, err := newRecoveryEventID()
			if err != nil {
				return err
			}
			observedUTC := time.Now().UTC()
			if _, err := repository.AppendEventAutoSequence(ctx, domain.AuditEvent{
				EventID: eventID, SessionID: sessionID, Category: domain.EventCategoryFile,
				Action: usnChangeAction(change.Reason), Severity: domain.EventSeverityLow,
				ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(), ObjectKey: path,
				Source: "ntfs_usn_journal", Confidence: domain.EventConfidenceCorrelated,
			}, payload); err != nil {
				return err
			}
		}
	}
	return nil
}

func usnChangeAction(reason uint32) string {
	switch {
	case reason&collector.USNReasonFileDelete != 0:
		return "file_deleted"
	case reason&collector.USNReasonFileCreate != 0:
		return "file_created"
	case reason&collector.USNReasonRenameNewName != 0:
		return "file_renamed"
	case reason&collector.USNReasonDataTruncation != 0:
		return "file_truncated"
	default:
		return "file_modified"
	}
}

func usnRecoveryPathMatches(targets []domain.MonitoringTarget, path string) bool {
	path = strings.ToLower(filepath.Clean(path))
	for _, target := range targets {
		root := strings.ToLower(filepath.Clean(target.Path))
		switch target.Kind {
		case domain.MonitoringTargetKindFile:
			if path == root {
				return true
			}
		case domain.MonitoringTargetKindDirectory:
			if path == root || strings.HasPrefix(path, strings.TrimRight(root, `\`)+`\`) {
				return target.Recursive || filepath.Dir(path) == root
			}
		case domain.MonitoringTargetKindRemovableVolume:
			if path == root || strings.HasPrefix(path, strings.TrimRight(root, `\`)+`\`) {
				return true
			}
		}
	}
	return false
}

func usnRecoveryGaps(start []storage.USNJournalCheckpoint, current []collector.USNJournalState) []usnRecoveryGap {
	byVolume := make(map[string]collector.USNJournalState, len(current))
	for _, state := range current {
		byVolume[state.Volume] = state
	}
	gaps := make([]usnRecoveryGap, 0, len(start))
	for _, checkpoint := range start {
		state, found := byVolume[checkpoint.Volume]
		if !found || state.JournalID != checkpoint.JournalID || state.NextUSN > checkpoint.NextUSN {
			gap := usnRecoveryGap{Volume: checkpoint.Volume, PreviousUSN: checkpoint.NextUSN}
			if found {
				gap.CurrentUSN, gap.JournalReset = state.NextUSN, state.JournalID != checkpoint.JournalID
			} else {
				gap.JournalReset = true
			}
			gaps = append(gaps, gap)
		}
	}
	return gaps
}

type fileBaselineDifference struct {
	Path   string
	Action string
	Before *storage.FileBaselineEntry
	After  *storage.FileBaselineEntry
}

type fileBaselineDifferencePayload struct {
	Reconciliation string                     `json:"reconciliation"`
	Before         *storage.FileBaselineEntry `json:"before,omitempty"`
	After          *storage.FileBaselineEntry `json:"after,omitempty"`
}

func recordFileBaselineRecoveryBoundary(ctx context.Context, repository *storage.Repository, session domain.Session) error {
	return nil
}

func appendFileBaselineDifferences(
	ctx context.Context,
	repository *storage.Repository,
	sessionID string,
	before, after []storage.FileBaselineEntry,
	reconciliation string,
) error {
	observedUTC := time.Now().UTC()
	for _, difference := range fileBaselineDifferences(before, after) {
		payload, err := json.Marshal(fileBaselineDifferencePayload{
			Reconciliation: reconciliation, Before: difference.Before, After: difference.After,
		})
		if err != nil {
			return fmt.Errorf("encode file baseline difference: %w", err)
		}
		eventID, err := newRecoveryEventID()
		if err != nil {
			return err
		}
		if _, err := repository.AppendEventAutoSequence(ctx, domain.AuditEvent{
			EventID: eventID, SessionID: sessionID, Category: domain.EventCategoryFile,
			Action: difference.Action, Severity: domain.EventSeverityMedium,
			ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(), ObjectKey: difference.Path,
			Source: "file_baseline_reconciliation", Confidence: domain.EventConfidenceSnapshotDiff,
		}, payload); err != nil {
			return err
		}
	}
	return nil
}

func storageFileBaselineEntries(entries []collector.FileBaselineEntry) []storage.FileBaselineEntry {
	converted := make([]storage.FileBaselineEntry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, storage.FileBaselineEntry{
			Path: entry.Path, Size: entry.Size, Mode: entry.Mode,
			CreatedUTC: time.Unix(0, entry.CreatedUTC).UTC(), AccessedUTC: time.Unix(0, entry.AccessedUTC).UTC(),
			ModifiedUTC: time.Unix(0, entry.ModifiedUTC).UTC(), ContentSHA256: entry.ContentSHA256, HashStatus: entry.HashStatus,
		})
	}
	return converted
}

func fileBaselineDifferences(before, after []storage.FileBaselineEntry) []fileBaselineDifference {
	beforeByPath := make(map[string]storage.FileBaselineEntry, len(before))
	for _, entry := range before {
		beforeByPath[entry.Path] = entry
	}
	afterByPath := make(map[string]storage.FileBaselineEntry, len(after))
	for _, entry := range after {
		afterByPath[entry.Path] = entry
	}
	paths := make([]string, 0, len(beforeByPath)+len(afterByPath))
	for path := range beforeByPath {
		paths = append(paths, path)
	}
	for path := range afterByPath {
		if _, found := beforeByPath[path]; !found {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	differences := make([]fileBaselineDifference, 0)
	for _, path := range paths {
		beforeEntry, hadBefore := beforeByPath[path]
		afterEntry, hadAfter := afterByPath[path]
		switch {
		case !hadBefore:
			entry := afterEntry
			differences = append(differences, fileBaselineDifference{Path: path, Action: "file_added", After: &entry})
		case !hadAfter:
			entry := beforeEntry
			differences = append(differences, fileBaselineDifference{Path: path, Action: "file_removed", Before: &entry})
		case !fileBaselineEntryEqual(beforeEntry, afterEntry):
			beforeCopy, afterCopy := beforeEntry, afterEntry
			differences = append(differences, fileBaselineDifference{Path: path, Action: "file_modified", Before: &beforeCopy, After: &afterCopy})
		}
	}
	return differences
}

func fileBaselineEntryEqual(left, right storage.FileBaselineEntry) bool {
	return left.Size == right.Size && left.Mode == right.Mode && left.CreatedUTC.Equal(right.CreatedUTC) && left.ModifiedUTC.Equal(right.ModifiedUTC) &&
		left.ContentSHA256 == right.ContentSHA256 && left.HashStatus == right.HashStatus
}

func newRecoveryEventID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create recovery event id: %w", err)
	}
	return "recovery-" + hex.EncodeToString(data), nil
}

func (runtime *applicationRuntime) Close() error {
	return runtime.database.Close()
}

func (runtime *applicationRuntime) RunRetention(ctx context.Context) error {
	return runRetentionScheduler(ctx, runtime.repository, time.Now, automaticRetentionInterval)
}

func runRetentionScheduler(
	ctx context.Context,
	pruner terminalSessionPruner,
	now func() time.Time,
	interval time.Duration,
) error {
	if pruner == nil || now == nil || interval <= 0 {
		return errors.New("retention scheduler dependency is invalid")
	}
	prune := func() error {
		_, err := pruner.PruneTerminalSessions(ctx, now().UTC().Add(-automaticRetentionPeriod), automaticRetentionPruneLimit)
		return err
	}
	if err := prune(); err != nil {
		return fmt.Errorf("run automatic session retention: %w", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := prune(); err != nil {
				return fmt.Errorf("run automatic session retention: %w", err)
			}
		}
	}
}
