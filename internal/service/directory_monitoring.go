package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
)

var ErrDirectoryMonitoringInvalid = errors.New("monitored directory configuration is invalid")

type DirectoryMonitoringResult struct {
	Directories              []string                     `json:"directories"`
	Targets                  []domain.MonitoringTarget    `json:"targets,omitempty"`
	Exclusions               []domain.MonitoringExclusion `json:"exclusions,omitempty"`
	MonitoringPolicy         *domain.MonitoringPolicy     `json:"monitoringPolicy,omitempty"`
	MonitoringProfiles       *domain.MonitoringProfiles   `json:"monitoringProfiles,omitempty"`
	ResetMonitoringMode      *domain.MonitoringMode       `json:"resetMonitoringMode,omitempty"`
	InputActivityEnabled     *bool                        `json:"inputActivityEnabled,omitempty"`
	WindowTitleEnabled       *bool                        `json:"windowTitleEnabled,omitempty"`
	HighRiskShortcutsEnabled *bool                        `json:"highRiskShortcutsEnabled,omitempty"`
}

type DirectoryMonitoringStore interface {
	LoadMonitoredDirectories(context.Context) ([]string, error)
	SaveMonitoredDirectories(context.Context, []string) error
}

type MonitoringTargetStore interface {
	LoadMonitoringTargets(context.Context) ([]domain.MonitoringTarget, error)
	SaveMonitoringTargets(context.Context, []domain.MonitoringTarget) error
}

type MonitoringExclusionStore interface {
	LoadMonitoringExclusions(context.Context) ([]domain.MonitoringExclusion, error)
	SaveMonitoringExclusions(context.Context, []domain.MonitoringExclusion) error
}

type MonitoringPolicyStore interface {
	LoadMonitoringPolicy(context.Context) (domain.MonitoringPolicy, error)
	SaveMonitoringPolicy(context.Context, domain.MonitoringPolicy) error
}

type MonitoringProfileStore interface {
	LoadMonitoringProfiles(context.Context) (domain.MonitoringProfiles, error)
	SaveMonitoringProfiles(context.Context, domain.MonitoringProfiles) error
}

type InputActivityPreferenceStore interface {
	LoadInputActivityEnabled(context.Context) (bool, error)
	SaveInputActivityEnabled(context.Context, bool) error
}

type WindowTitlePreferenceStore interface {
	LoadWindowTitleEnabled(context.Context) (bool, error)
	SaveWindowTitleEnabled(context.Context, bool) error
}

type HighRiskShortcutsPreferenceStore interface {
	LoadHighRiskShortcutsEnabled(context.Context) (bool, error)
	SaveHighRiskShortcutsEnabled(context.Context, bool) error
}

type monitoringRuleEventStore interface {
	AppendEventAutoSequence(context.Context, domain.AuditEvent, []byte) (domain.AuditEvent, error)
}

func (api *API) SetDirectoryMonitoringValidator(validate func([]string) ([]string, error)) {
	api.coordinator.mu.Lock()
	defer api.coordinator.mu.Unlock()
	api.directoryMonitoringValidator = validate
}

func (api *API) SetMonitoringTargetValidator(validate func([]domain.MonitoringTarget) ([]domain.MonitoringTarget, error)) {
	api.coordinator.mu.Lock()
	defer api.coordinator.mu.Unlock()
	api.monitoringTargetValidator = validate
}

// Called while creating a session under the coordinator lock, before any
// session state is persisted. A stale directory selection remains editable.
func (api *API) validateDirectoryMonitoring(ctx context.Context) error {
	store, ok := api.store.(DirectoryMonitoringStore)
	if !ok {
		return nil
	}
	if targetStore, ok := api.store.(MonitoringTargetStore); ok && api.monitoringTargetValidator != nil {
		targets, err := targetStore.LoadMonitoringTargets(ctx)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
		}
		if _, err := api.monitoringTargetValidator(targets); err != nil {
			return fmt.Errorf("%w: %v", ErrDirectoryMonitoringInvalid, err)
		}
		return nil
	}
	if api.directoryMonitoringValidator == nil {
		return nil
	}
	directories, err := api.loadMonitoredDirectories(ctx, store)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
	}
	if _, err := api.directoryMonitoringValidator(directories); err != nil {
		return fmt.Errorf("%w: %v", ErrDirectoryMonitoringInvalid, err)
	}
	return nil
}

func (api *API) getDirectoryMonitoring(request contracts.Message) (contracts.Message, error) {
	store, ok := api.store.(DirectoryMonitoringStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "重点目录配置暂不可用")
	}
	ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
	defer cancel()
	directories, err := store.LoadMonitoredDirectories(ctx)
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法读取")
	}
	result := DirectoryMonitoringResult{Directories: directories}
	if targetStore, ok := api.store.(MonitoringTargetStore); ok {
		targets, err := targetStore.LoadMonitoringTargets(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法读取")
		}
		result.Targets = domain.CloneMonitoringTargets(targets)
	}
	if exclusionStore, ok := api.store.(MonitoringExclusionStore); ok {
		exclusions, err := exclusionStore.LoadMonitoringExclusions(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录排除规则无法读取")
		}
		result.Exclusions = append([]domain.MonitoringExclusion(nil), exclusions...)
	}
	if policyStore, ok := api.store.(MonitoringPolicyStore); ok {
		policy, err := policyStore.LoadMonitoringPolicy(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "监控与审计策略无法读取")
		}
		result.MonitoringPolicy = &policy
	}
	if profileStore, ok := api.store.(MonitoringProfileStore); ok {
		profiles, err := profileStore.LoadMonitoringProfiles(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "监控模式配置无法读取")
		}
		result.MonitoringProfiles = &profiles
	}
	if preferenceStore, ok := api.store.(InputActivityPreferenceStore); ok {
		enabled, err := preferenceStore.LoadInputActivityEnabled(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "输入活动设置无法读取")
		}
		result.InputActivityEnabled = &enabled
	}
	if preferenceStore, ok := api.store.(WindowTitlePreferenceStore); ok {
		enabled, err := preferenceStore.LoadWindowTitleEnabled(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "窗口标题设置无法读取")
		}
		result.WindowTitleEnabled = &enabled
	}
	if preferenceStore, ok := api.store.(HighRiskShortcutsPreferenceStore); ok {
		enabled, err := preferenceStore.LoadHighRiskShortcutsEnabled(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "高风险组合键设置无法读取")
		}
		result.HighRiskShortcutsEnabled = &enabled
	}
	return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, result)
}

func (api *API) updateDirectoryMonitoring(request contracts.Message) (contracts.Message, error) {
	var payload DirectoryMonitoringResult
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "重点目录配置格式无效")
	}
	// Share the coordinator lock with session creation so configuration cannot
	// change between the idle check and a new protection session starting.
	api.coordinator.mu.Lock()
	defer api.coordinator.mu.Unlock()
	store, ok := api.store.(DirectoryMonitoringStore)
	if !ok || (api.directoryMonitoringValidator == nil && api.monitoringTargetValidator == nil) {
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "重点目录配置暂不可用")
	}
	activeSession := api.coordinator.current
	if activeSession != nil && isTerminal(activeSession.State) {
		activeSession = nil
	}
	if payload.ResetMonitoringMode != nil {
		profileStore, ok := api.store.(MonitoringProfileStore)
		if !ok {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "监控模式配置暂不可用")
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		profiles, err := profileStore.LoadMonitoringProfiles(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "监控模式配置无法读取")
		}
		previous, err := profiles.Policy(*payload.ResetMonitoringMode)
		if err != nil {
			return api.domainErrorResponse(request, err)
		}
		profiles, err = profiles.Reset(*payload.ResetMonitoringMode)
		if err != nil {
			return api.domainErrorResponse(request, err)
		}
		if err := profileStore.SaveMonitoringProfiles(ctx, profiles); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "监控模式配置无法重置")
		}
		policy, _ := profiles.Policy(*payload.ResetMonitoringMode)
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "monitoring_profile", previous, policy); err != nil {
			return api.domainErrorResponse(request, err)
		}
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{
			MonitoringPolicy: &policy, MonitoringProfiles: &profiles,
		})
	}
	if payload.MonitoringPolicy != nil {
		policyStore, hasLegacyStore := api.store.(MonitoringPolicyStore)
		profileStore, hasProfileStore := api.store.(MonitoringProfileStore)
		if !hasLegacyStore && !hasProfileStore {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "监控与审计策略暂不可用")
		}
		policy := payload.MonitoringPolicy.Resolved()
		policy.InputShieldEnabled = false
		if err := policy.Validate(); err != nil {
			return api.domainErrorResponse(request, err)
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		resolved := policy
		var previous domain.MonitoringPolicy
		var profiles *domain.MonitoringProfiles
		if hasProfileStore {
			loaded, err := profileStore.LoadMonitoringProfiles(ctx)
			if err != nil {
				return api.errorResponse(request, ErrorCodeStorageFailure, "监控模式配置无法读取")
			}
			mode := resolved.Mode
			previous, err = loaded.Policy(mode)
			if err != nil {
				return api.domainErrorResponse(request, err)
			}
			loaded, err = loaded.WithPolicy(mode, policy)
			if err != nil {
				return api.domainErrorResponse(request, err)
			}
			if err := profileStore.SaveMonitoringProfiles(ctx, loaded); err != nil {
				return api.errorResponse(request, ErrorCodeStorageFailure, "监控模式配置无法保存")
			}
			profiles = &loaded
		} else {
			var err error
			previous, err = policyStore.LoadMonitoringPolicy(ctx)
			if err != nil {
				return api.errorResponse(request, ErrorCodeStorageFailure, "监控与审计策略无法读取")
			}
			if err := policyStore.SaveMonitoringPolicy(ctx, resolved); err != nil {
				return api.errorResponse(request, ErrorCodeStorageFailure, "监控与审计策略无法保存")
			}
		}
		if err := api.saveLegacyUserSessionPreferences(ctx, resolved.UserSession); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "用户会话活动设置无法同步")
		}
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "monitoring_policy", previous, resolved); err != nil {
			return api.domainErrorResponse(request, err)
		}
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{
			MonitoringPolicy: &resolved, MonitoringProfiles: profiles,
		})
	}
	if payload.InputActivityEnabled != nil {
		preferenceStore, ok := api.store.(InputActivityPreferenceStore)
		if !ok {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "输入活动设置暂不可用")
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		previous, err := preferenceStore.LoadInputActivityEnabled(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "输入活动设置无法读取")
		}
		if err := preferenceStore.SaveInputActivityEnabled(ctx, *payload.InputActivityEnabled); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "输入活动设置无法保存")
		}
		if err := api.updateUserSessionMonitoringPolicy(ctx, func(policy *domain.UserSessionPolicy) {
			policy.RecordKeyboardActivity = *payload.InputActivityEnabled
			policy.RecordMouseClicks = *payload.InputActivityEnabled
			policy.RecordMouseWheel = *payload.InputActivityEnabled
		}); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "输入活动策略无法保存")
		}
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "input_activity", previous, *payload.InputActivityEnabled); err != nil {
			return api.domainErrorResponse(request, err)
		}
		value := *payload.InputActivityEnabled
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{InputActivityEnabled: &value})
	}
	if payload.HighRiskShortcutsEnabled != nil {
		preferenceStore, ok := api.store.(HighRiskShortcutsPreferenceStore)
		if !ok {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "高风险组合键设置暂不可用")
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		previous, err := preferenceStore.LoadHighRiskShortcutsEnabled(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "高风险组合键设置无法读取")
		}
		if err := preferenceStore.SaveHighRiskShortcutsEnabled(ctx, *payload.HighRiskShortcutsEnabled); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "高风险组合键设置无法保存")
		}
		if err := api.updateUserSessionMonitoringPolicy(ctx, func(policy *domain.UserSessionPolicy) {
			policy.RecordHighRiskShortcuts = *payload.HighRiskShortcutsEnabled
		}); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "高风险组合键策略无法保存")
		}
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "high_risk_shortcuts", previous, *payload.HighRiskShortcutsEnabled); err != nil {
			return api.domainErrorResponse(request, err)
		}
		value := *payload.HighRiskShortcutsEnabled
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{HighRiskShortcutsEnabled: &value})
	}
	if payload.WindowTitleEnabled != nil {
		preferenceStore, ok := api.store.(WindowTitlePreferenceStore)
		if !ok {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "窗口标题设置暂不可用")
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		previous, err := preferenceStore.LoadWindowTitleEnabled(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "窗口标题设置无法读取")
		}
		if err := preferenceStore.SaveWindowTitleEnabled(ctx, *payload.WindowTitleEnabled); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "窗口标题设置无法保存")
		}
		if err := api.updateUserSessionMonitoringPolicy(ctx, func(policy *domain.UserSessionPolicy) {
			policy.RecordWindowTitle = *payload.WindowTitleEnabled
		}); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "窗口标题策略无法保存")
		}
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "window_title", previous, *payload.WindowTitleEnabled); err != nil {
			return api.domainErrorResponse(request, err)
		}
		value := *payload.WindowTitleEnabled
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{WindowTitleEnabled: &value})
	}
	if payload.Exclusions != nil {
		exclusionStore, ok := api.store.(MonitoringExclusionStore)
		if !ok {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "重点目录排除规则暂不可用")
		}
		if err := domain.ValidateMonitoringExclusions(payload.Exclusions); err != nil {
			return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		previous, err := exclusionStore.LoadMonitoringExclusions(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录排除规则无法读取")
		}
		if err := exclusionStore.SaveMonitoringExclusions(ctx, payload.Exclusions); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录排除规则无法保存")
		}
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "exclusions", previous, payload.Exclusions); err != nil {
			return api.domainErrorResponse(request, err)
		}
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{
			Exclusions: append([]domain.MonitoringExclusion(nil), payload.Exclusions...),
		})
	}
	if payload.Targets != nil {
		targetStore, ok := api.store.(MonitoringTargetStore)
		if !ok {
			return api.errorResponse(request, ErrorCodeUnsupportedMessage, "重点目录规则暂不可用")
		}
		if err := domain.ValidateMonitoringTargets(payload.Targets); err != nil {
			return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
		}
		targets := domain.CloneMonitoringTargets(payload.Targets)
		if api.monitoringTargetValidator != nil {
			var err error
			targets, err = api.monitoringTargetValidator(targets)
			if err != nil {
				return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
			}
		}
		directories := monitoringTargetDirectories(targets)
		if api.directoryMonitoringValidator != nil {
			if _, err := api.directoryMonitoringValidator(directories); err != nil {
				return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
			}
		}
		ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
		defer cancel()
		previous, err := targetStore.LoadMonitoringTargets(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法读取")
		}
		if err := targetStore.SaveMonitoringTargets(ctx, targets); err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法保存")
		}
		if err := api.recordMonitoringRuleChange(ctx, activeSession, "targets", previous, targets); err != nil {
			return api.domainErrorResponse(request, err)
		}
		return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{
			Directories: directories, Targets: domain.CloneMonitoringTargets(targets),
		})
	}
	if api.directoryMonitoringValidator == nil {
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "重点目录配置暂不可用")
	}
	directories, err := api.directoryMonitoringValidator(payload.Directories)
	if err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
	}
	ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
	defer cancel()
	previous, err := store.LoadMonitoredDirectories(ctx)
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法读取")
	}
	if err := store.SaveMonitoredDirectories(ctx, directories); err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法保存")
	}
	if err := api.recordMonitoringRuleChange(ctx, activeSession, "directories", previous, directories); err != nil {
		return api.domainErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeDirectoryMonitoringResult, DirectoryMonitoringResult{Directories: directories})
}

func (api *API) updateUserSessionMonitoringPolicy(ctx context.Context, update func(*domain.UserSessionPolicy)) error {
	store, ok := api.store.(MonitoringPolicyStore)
	if !ok {
		return nil
	}
	policy, err := store.LoadMonitoringPolicy(ctx)
	if err != nil {
		return err
	}
	policy = policy.Resolved()
	update(&policy.UserSession)
	if err := policy.Validate(); err != nil {
		return err
	}
	return store.SaveMonitoringPolicy(ctx, policy)
}

func (api *API) saveLegacyUserSessionPreferences(ctx context.Context, policy domain.UserSessionPolicy) error {
	if store, ok := api.store.(InputActivityPreferenceStore); ok {
		enabled := policy.RecordKeyboardActivity || policy.RecordMouseClicks || policy.RecordMouseWheel
		if err := store.SaveInputActivityEnabled(ctx, enabled); err != nil {
			return err
		}
	}
	if store, ok := api.store.(WindowTitlePreferenceStore); ok {
		if err := store.SaveWindowTitleEnabled(ctx, policy.RecordWindowTitle); err != nil {
			return err
		}
	}
	if store, ok := api.store.(HighRiskShortcutsPreferenceStore); ok {
		if err := store.SaveHighRiskShortcutsEnabled(ctx, policy.RecordHighRiskShortcuts); err != nil {
			return err
		}
	}
	return nil
}

func (api *API) recordMonitoringRuleChange(
	ctx context.Context,
	session *domain.Session,
	ruleType string,
	previous any,
	current any,
) error {
	if session == nil {
		return nil
	}
	store, ok := api.store.(monitoringRuleEventStore)
	if !ok {
		return fmt.Errorf("%w: monitoring rule event store is unavailable", ErrSessionPersistence)
	}
	observedUTC := api.now().UTC()
	payload, err := json.Marshal(struct {
		RuleType string    `json:"ruleType"`
		Previous any       `json:"previous"`
		Current  any       `json:"current"`
		Observed time.Time `json:"observedUtc"`
	}{RuleType: ruleType, Previous: previous, Current: current, Observed: observedUTC})
	if err != nil {
		return fmt.Errorf("%w: encode monitoring rule event: %v", ErrSessionPersistence, err)
	}
	eventID, err := newSessionLifecycleEventID()
	if err != nil {
		return err
	}
	if _, err := store.AppendEventAutoSequence(ctx, domain.AuditEvent{
		EventID: eventID, SessionID: session.ID, Category: domain.EventCategorySystem,
		Action: "monitoring_rules_changed", Severity: domain.EventSeverityMedium,
		ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(),
		Source: "desktop_guard_service", Confidence: domain.EventConfidenceDirect,
	}, payload); err != nil {
		return fmt.Errorf("%w: record monitoring rule change: %v", ErrSessionPersistence, err)
	}
	return nil
}

func (api *API) loadMonitoredDirectories(ctx context.Context, store DirectoryMonitoringStore) ([]string, error) {
	if targetStore, ok := api.store.(MonitoringTargetStore); ok {
		targets, err := targetStore.LoadMonitoringTargets(ctx)
		if err != nil {
			return nil, err
		}
		if err := domain.ValidateMonitoringTargets(targets); err != nil {
			return nil, err
		}
		return monitoringTargetDirectories(targets), nil
	}
	return store.LoadMonitoredDirectories(ctx)
}

func monitoringTargetDirectories(targets []domain.MonitoringTarget) []string {
	directories := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Kind == domain.MonitoringTargetKindDirectory {
			directories = append(directories, target.Path)
		}
	}
	return directories
}
