package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrMonitoringPolicyRequiresCapability  = errors.New("monitoring policy requires an enabled capability")
	ErrStrictReadAuditRequiresFileActivity = errors.New("strict read audit requires file activity monitoring")
	ErrMonitoringPolicyVersionUnsupported  = errors.New("monitoring policy version is unsupported")
	ErrMonitoringModeInvalid               = errors.New("monitoring mode is invalid")
	ErrMonitoringPolicyDetailInvalid       = errors.New("monitoring policy detail is invalid")
)

const CurrentMonitoringPolicyVersion = 3

type MonitoringMode string

const (
	MonitoringModeRelaxed  MonitoringMode = "relaxed"
	MonitoringModeStandard MonitoringMode = "standard"
	MonitoringModeStrict   MonitoringMode = "strict"
	MonitoringModeCustom   MonitoringMode = "custom"
)

type InjectedInputMode string

const (
	InjectedInputModeCompatible InjectedInputMode = "compatible"
	InjectedInputModeRestricted InjectedInputMode = "restricted"
	InjectedInputModeStrict     InjectedInputMode = "strict"
)

type InputShieldUnlockTrigger string

const (
	InputShieldUnlockTriggerCombination InputShieldUnlockTrigger = "combination"
	InputShieldUnlockTriggerTap         InputShieldUnlockTrigger = "tap"
)

type InputShieldCredentialMode string

const (
	InputShieldCredentialWindows InputShieldCredentialMode = "windows"
	InputShieldCredentialLocal   InputShieldCredentialMode = "local"
)

type InputShieldUnlockAction string

const (
	InputShieldUnlockActionSuspend    InputShieldUnlockAction = "suspend"
	InputShieldUnlockActionEndSession InputShieldUnlockAction = "end_session"
)

type FileAuditPolicy struct {
	RecordCreate       bool `json:"recordCreate"`
	RecordModify       bool `json:"recordModify"`
	RecordDelete       bool `json:"recordDelete"`
	RecordRename       bool `json:"recordRename"`
	CaptureContentHash bool `json:"captureContentHash"`
	CaptureBaseline    bool `json:"captureBaseline"`
}

type ProcessSoftwarePolicy struct {
	RecordProcessStart      bool `json:"recordProcessStart"`
	RecordProcessStop       bool `json:"recordProcessStop"`
	DetectInstallers        bool `json:"detectInstallers"`
	DetectSoftwareRepair    bool `json:"detectSoftwareRepair"`
	CaptureImageMetadata    bool `json:"captureImageMetadata"`
	SnapshotIntervalSeconds int  `json:"snapshotIntervalSeconds"`
}

type SystemNetworkPolicy struct {
	MonitorAccounts         bool `json:"monitorAccounts"`
	MonitorNetwork          bool `json:"monitorNetwork"`
	MonitorProxy            bool `json:"monitorProxy"`
	MonitorFirewall         bool `json:"monitorFirewall"`
	MonitorRemoteDesktop    bool `json:"monitorRemoteDesktop"`
	MonitorAuditPolicy      bool `json:"monitorAuditPolicy"`
	MonitorSecurityLog      bool `json:"monitorSecurityLog"`
	MonitorClock            bool `json:"monitorClock"`
	MonitorServices         bool `json:"monitorServices"`
	MonitorDrivers          bool `json:"monitorDrivers"`
	MonitorScheduledTasks   bool `json:"monitorScheduledTasks"`
	MonitorStartupItems     bool `json:"monitorStartupItems"`
	SnapshotIntervalSeconds int  `json:"snapshotIntervalSeconds"`
}

type ExternalDevicePolicy struct {
	RecordConnect           bool `json:"recordConnect"`
	RecordDisconnect        bool `json:"recordDisconnect"`
	SnapshotIntervalSeconds int  `json:"snapshotIntervalSeconds"`
}

type UserSessionPolicy struct {
	RecordForegroundApplication bool `json:"recordForegroundApplication"`
	RecordWindowTitle           bool `json:"recordWindowTitle"`
	RecordKeyboardActivity      bool `json:"recordKeyboardActivity"`
	RecordMouseClicks           bool `json:"recordMouseClicks"`
	RecordMouseWheel            bool `json:"recordMouseWheel"`
	RecordHighRiskShortcuts     bool `json:"recordHighRiskShortcuts"`
	SampleIntervalSeconds       int  `json:"sampleIntervalSeconds"`
	ReportIntervalSeconds       int  `json:"reportIntervalSeconds"`
}

type InputShieldPolicy struct {
	BlockPhysicalKeyboard       bool                      `json:"blockPhysicalKeyboard"`
	BlockPhysicalMouse          bool                      `json:"blockPhysicalMouse"`
	BlockPointerMovement        bool                      `json:"blockPointerMovement"`
	InjectedInputMode           InjectedInputMode         `json:"injectedInputMode"`
	ShowWarningOverlay          bool                      `json:"showWarningOverlay"`
	WarningDurationSeconds      int                       `json:"warningDurationSeconds"`
	WarningMessage              string                    `json:"warningMessage"`
	TrackActiveDevices          bool                      `json:"trackActiveDevices"`
	WarnOnDeviceArrival         bool                      `json:"warnOnDeviceArrival"`
	RecordDeviceRemoval         bool                      `json:"recordDeviceRemoval"`
	UnlockTrigger               InputShieldUnlockTrigger  `json:"unlockTrigger"`
	UnlockKeyCode               int                       `json:"unlockKeyCode"`
	UnlockRequireControl        bool                      `json:"unlockRequireControl"`
	UnlockRequireAlt            bool                      `json:"unlockRequireAlt"`
	UnlockRequireShift          bool                      `json:"unlockRequireShift"`
	UnlockTapCount              int                       `json:"unlockTapCount"`
	UnlockTapWindowMilliseconds int                       `json:"unlockTapWindowMilliseconds"`
	CredentialMode              InputShieldCredentialMode `json:"credentialMode"`
	AllowRecoveryCode           bool                      `json:"allowRecoveryCode"`
	UnlockAction                InputShieldUnlockAction   `json:"unlockAction"`
	MaxFailedUnlockAttempts     int                       `json:"maxFailedUnlockAttempts"`
	UnlockLockoutSeconds        int                       `json:"unlockLockoutSeconds"`
	RecordBlockedInputCategory  bool                      `json:"recordBlockedInputCategory"`
	RecordKeyNames              bool                      `json:"recordKeyNames"`
	RecordPointerCoordinates    bool                      `json:"recordPointerCoordinates"`
	RestoreAfterRestart         bool                      `json:"restoreAfterRestart"`
	HookHeartbeatSeconds        int                       `json:"hookHeartbeatSeconds"`
}

type RiskRulePolicy struct {
	FileActivity     bool `json:"fileActivity"`
	SensitiveProcess bool `json:"sensitiveProcess"`
	StartupChange    bool `json:"startupChange"`
	SoftwareChange   bool `json:"softwareChange"`
	DeviceConnection bool `json:"deviceConnection"`
	CollectionGap    bool `json:"collectionGap"`
	SecurityState    bool `json:"securityState"`
}

// MonitoringPolicy defines the audit capabilities applied when a protection
// session is created. A session retains its own policy snapshot.
type MonitoringPolicy struct {
	Version                    int                   `json:"version,omitempty"`
	Mode                       MonitoringMode        `json:"mode,omitempty"`
	FileActivityEnabled        bool                  `json:"fileActivityEnabled"`
	ProcessAndSoftwareEnabled  bool                  `json:"processAndSoftwareEnabled"`
	SystemAndNetworkEnabled    bool                  `json:"systemAndNetworkEnabled"`
	ExternalDevicesEnabled     bool                  `json:"externalDevicesEnabled"`
	UserSessionActivityEnabled bool                  `json:"userSessionActivityEnabled"`
	InputShieldEnabled         bool                  `json:"inputShieldEnabled"`
	StrictReadAuditEnabled     bool                  `json:"strictReadAuditEnabled"`
	File                       FileAuditPolicy       `json:"file"`
	ProcessAndSoftware         ProcessSoftwarePolicy `json:"processAndSoftware"`
	SystemAndNetwork           SystemNetworkPolicy   `json:"systemAndNetwork"`
	ExternalDevices            ExternalDevicePolicy  `json:"externalDevices"`
	UserSession                UserSessionPolicy     `json:"userSession"`
	InputShield                InputShieldPolicy     `json:"inputShield"`
	RiskRules                  RiskRulePolicy        `json:"riskRules"`
}

type MonitoringProfiles struct {
	Relaxed  MonitoringPolicy `json:"relaxed"`
	Standard MonitoringPolicy `json:"standard"`
	Strict   MonitoringPolicy `json:"strict"`
	Custom   MonitoringPolicy `json:"custom"`
}

func DefaultMonitoringProfiles() (MonitoringProfiles, error) {
	relaxed, err := BuiltInMonitoringPolicy(MonitoringModeRelaxed)
	if err != nil {
		return MonitoringProfiles{}, err
	}
	standard, err := BuiltInMonitoringPolicy(MonitoringModeStandard)
	if err != nil {
		return MonitoringProfiles{}, err
	}
	strict, err := BuiltInMonitoringPolicy(MonitoringModeStrict)
	if err != nil {
		return MonitoringProfiles{}, err
	}
	custom := DefaultMonitoringPolicy()
	custom.Mode = MonitoringModeCustom
	return MonitoringProfiles{Relaxed: relaxed, Standard: standard, Strict: strict, Custom: custom}, nil
}

func (profiles MonitoringProfiles) Resolved() (MonitoringProfiles, error) {
	defaults, err := DefaultMonitoringProfiles()
	if err != nil {
		return MonitoringProfiles{}, err
	}
	for _, entry := range []struct {
		mode   MonitoringMode
		stored *MonitoringPolicy
		value  MonitoringPolicy
	}{
		{MonitoringModeRelaxed, &profiles.Relaxed, defaults.Relaxed},
		{MonitoringModeStandard, &profiles.Standard, defaults.Standard},
		{MonitoringModeStrict, &profiles.Strict, defaults.Strict},
		{MonitoringModeCustom, &profiles.Custom, defaults.Custom},
	} {
		policy := *entry.stored
		if policy == (MonitoringPolicy{}) {
			policy = entry.value
		} else {
			policy = policy.Resolved()
			policy.Mode = entry.mode
		}
		policy.InputShieldEnabled = false
		if err := policy.Validate(); err != nil {
			return MonitoringProfiles{}, err
		}
		*entry.stored = policy
	}
	return profiles, nil
}

func (profiles MonitoringProfiles) Policy(mode MonitoringMode) (MonitoringPolicy, error) {
	resolved, err := profiles.Resolved()
	if err != nil {
		return MonitoringPolicy{}, err
	}
	switch mode {
	case MonitoringModeRelaxed:
		return resolved.Relaxed, nil
	case MonitoringModeStandard:
		return resolved.Standard, nil
	case MonitoringModeStrict:
		return resolved.Strict, nil
	case MonitoringModeCustom:
		return resolved.Custom, nil
	default:
		return MonitoringPolicy{}, ErrMonitoringModeInvalid
	}
}

func (profiles MonitoringProfiles) WithPolicy(mode MonitoringMode, policy MonitoringPolicy) (MonitoringProfiles, error) {
	resolved, err := profiles.Resolved()
	if err != nil {
		return MonitoringProfiles{}, err
	}
	policy = policy.Resolved()
	policy.Mode = mode
	policy.InputShieldEnabled = false
	if err := policy.Validate(); err != nil {
		return MonitoringProfiles{}, err
	}
	switch mode {
	case MonitoringModeRelaxed:
		resolved.Relaxed = policy
	case MonitoringModeStandard:
		resolved.Standard = policy
	case MonitoringModeStrict:
		resolved.Strict = policy
	case MonitoringModeCustom:
		resolved.Custom = policy
	default:
		return MonitoringProfiles{}, ErrMonitoringModeInvalid
	}
	return resolved, nil
}

func (profiles MonitoringProfiles) Reset(mode MonitoringMode) (MonitoringProfiles, error) {
	defaults, err := DefaultMonitoringProfiles()
	if err != nil {
		return MonitoringProfiles{}, err
	}
	policy, err := defaults.Policy(mode)
	if err != nil {
		return MonitoringProfiles{}, err
	}
	return profiles.WithPolicy(mode, policy)
}

func DefaultMonitoringPolicy() MonitoringPolicy {
	return MonitoringPolicy{
		Version:                    CurrentMonitoringPolicyVersion,
		Mode:                       MonitoringModeStandard,
		FileActivityEnabled:        true,
		ProcessAndSoftwareEnabled:  true,
		SystemAndNetworkEnabled:    true,
		ExternalDevicesEnabled:     true,
		UserSessionActivityEnabled: false,
		File: FileAuditPolicy{
			RecordCreate: true, RecordModify: true, RecordDelete: true, RecordRename: true,
			CaptureContentHash: true,
		},
		ProcessAndSoftware: ProcessSoftwarePolicy{
			SnapshotIntervalSeconds: 300,
		},
		SystemAndNetwork: SystemNetworkPolicy{
			MonitorAccounts: true, MonitorNetwork: true, MonitorProxy: true, MonitorFirewall: true,
			MonitorRemoteDesktop: true, MonitorAuditPolicy: true, MonitorSecurityLog: true, MonitorClock: true,
			MonitorServices: true, MonitorDrivers: true, MonitorScheduledTasks: true, MonitorStartupItems: true,
			SnapshotIntervalSeconds: 30,
		},
		ExternalDevices: ExternalDevicePolicy{
			RecordConnect: true, RecordDisconnect: true, SnapshotIntervalSeconds: 5,
		},
		UserSession: UserSessionPolicy{
			RecordForegroundApplication: true, RecordKeyboardActivity: true,
			RecordMouseClicks: true, RecordMouseWheel: true,
			SampleIntervalSeconds: 5, ReportIntervalSeconds: 30,
		},
		InputShield: InputShieldPolicy{
			BlockPhysicalKeyboard: true, BlockPhysicalMouse: true, BlockPointerMovement: true,
			InjectedInputMode:  InjectedInputModeCompatible,
			ShowWarningOverlay: true, WarningDurationSeconds: 5,
			WarningMessage:     "检测到本地键盘或鼠标输入，当前设备处于保护状态。",
			TrackActiveDevices: true, WarnOnDeviceArrival: true, RecordDeviceRemoval: true,
			UnlockTrigger: InputShieldUnlockTriggerCombination, UnlockKeyCode: 0x20,
			UnlockRequireControl: true, UnlockRequireAlt: true,
			UnlockTapCount: 5, UnlockTapWindowMilliseconds: 1500,
			CredentialMode: InputShieldCredentialWindows, UnlockAction: InputShieldUnlockActionSuspend,
			MaxFailedUnlockAttempts: 5, UnlockLockoutSeconds: 60,
			RecordBlockedInputCategory: true, HookHeartbeatSeconds: 5,
		},
		RiskRules: RiskRulePolicy{
			FileActivity: true, SensitiveProcess: true, StartupChange: true, SoftwareChange: true,
			DeviceConnection: true, CollectionGap: true, SecurityState: true,
		},
	}
}

// BuiltInMonitoringPolicy returns an immutable product preset. Active input
// blocking and sensitive key/coordinate recording always require a separate,
// explicit laboratory action and are therefore disabled in every preset.
func BuiltInMonitoringPolicy(mode MonitoringMode) (MonitoringPolicy, error) {
	policy := DefaultMonitoringPolicy()
	policy.Mode = mode
	switch mode {
	case MonitoringModeRelaxed:
		policy.ProcessAndSoftwareEnabled = false
		policy.SystemAndNetworkEnabled = false
		policy.UserSessionActivityEnabled = false
		policy.File.CaptureContentHash = false
		policy.File.CaptureBaseline = false
		policy.RiskRules.SensitiveProcess = false
		policy.RiskRules.StartupChange = false
		policy.RiskRules.SoftwareChange = false
		policy.RiskRules.SecurityState = false
	case MonitoringModeStandard:
	case MonitoringModeStrict:
		policy.StrictReadAuditEnabled = true
		policy.SystemAndNetwork.SnapshotIntervalSeconds = 10
		policy.ExternalDevices.SnapshotIntervalSeconds = 2
		policy.UserSession.RecordWindowTitle = true
		policy.UserSession.RecordHighRiskShortcuts = true
		policy.UserSession.SampleIntervalSeconds = 2
		policy.UserSession.ReportIntervalSeconds = 10
	default:
		return MonitoringPolicy{}, ErrMonitoringModeInvalid
	}
	policy.InputShieldEnabled = false
	policy.InputShield.RecordKeyNames = false
	policy.InputShield.RecordPointerCoordinates = false
	return policy, nil
}

// Resolved upgrades a legacy policy that predates detailed module settings.
// Existing top-level capability choices are preserved while details receive
// the defaults that those older releases used internally.
func (policy MonitoringPolicy) Resolved() MonitoringPolicy {
	if policy.Version == CurrentMonitoringPolicyVersion {
		if policy.Mode == "" {
			policy.Mode = MonitoringModeCustom
		}
		return policy.withRemovedAuditDetailsDisabled()
	}
	if policy.Version == 2 {
		resolved := policy
		resolved.Version = CurrentMonitoringPolicyVersion
		resolved.UserSessionActivityEnabled = false
		resolved.InputShieldEnabled = false
		if resolved.ProcessAndSoftware.SnapshotIntervalSeconds < 60 {
			resolved.ProcessAndSoftware.SnapshotIntervalSeconds = 300
		}
		if resolved.Mode == "" {
			resolved.Mode = MonitoringModeCustom
		}
		return resolved.withRemovedAuditDetailsDisabled()
	}
	if policy.Version == 1 {
		resolved := policy
		resolved.Version = CurrentMonitoringPolicyVersion
		resolved.Mode = MonitoringModeCustom
		resolved.InputShieldEnabled = false
		resolved.InputShield = DefaultMonitoringPolicy().InputShield
		resolved.UserSessionActivityEnabled = false
		return resolved.withRemovedAuditDetailsDisabled()
	}
	if policy.Version != 0 {
		return policy
	}
	resolved := DefaultMonitoringPolicy()
	resolved.Mode = MonitoringModeCustom
	resolved.FileActivityEnabled = policy.FileActivityEnabled
	resolved.ProcessAndSoftwareEnabled = policy.ProcessAndSoftwareEnabled
	resolved.SystemAndNetworkEnabled = policy.SystemAndNetworkEnabled
	resolved.ExternalDevicesEnabled = policy.ExternalDevicesEnabled
	resolved.UserSessionActivityEnabled = policy.UserSessionActivityEnabled
	resolved.InputShieldEnabled = false
	resolved.StrictReadAuditEnabled = policy.StrictReadAuditEnabled
	return resolved.withRemovedAuditDetailsDisabled()
}

func (policy MonitoringPolicy) withRemovedAuditDetailsDisabled() MonitoringPolicy {
	policy.File.CaptureBaseline = false
	policy.ProcessAndSoftware.RecordProcessStart = false
	policy.ProcessAndSoftware.RecordProcessStop = false
	policy.ProcessAndSoftware.DetectInstallers = false
	policy.ProcessAndSoftware.DetectSoftwareRepair = false
	policy.ProcessAndSoftware.CaptureImageMetadata = false
	return policy
}

func (policy MonitoringPolicy) Validate() error {
	policy = policy.Resolved()
	if policy.Version != CurrentMonitoringPolicyVersion {
		return ErrMonitoringPolicyVersionUnsupported
	}
	switch policy.Mode {
	case MonitoringModeRelaxed, MonitoringModeStandard, MonitoringModeStrict, MonitoringModeCustom:
	default:
		return ErrMonitoringModeInvalid
	}
	if !policy.FileActivityEnabled && !policy.ProcessAndSoftwareEnabled &&
		!policy.SystemAndNetworkEnabled && !policy.ExternalDevicesEnabled &&
		!policy.UserSessionActivityEnabled && !policy.InputShieldEnabled {
		return ErrMonitoringPolicyRequiresCapability
	}
	if policy.StrictReadAuditEnabled && !policy.FileActivityEnabled {
		return ErrStrictReadAuditRequiresFileActivity
	}
	if policy.FileActivityEnabled && !policy.File.RecordCreate && !policy.File.RecordModify &&
		!policy.File.RecordDelete && !policy.File.RecordRename && !policy.File.CaptureBaseline &&
		!policy.StrictReadAuditEnabled {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.ProcessAndSoftwareEnabled &&
		(policy.ProcessAndSoftware.SnapshotIntervalSeconds < 60 || policy.ProcessAndSoftware.SnapshotIntervalSeconds > 3600) {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.SystemAndNetworkEnabled && !policy.SystemAndNetwork.anyEnabled() {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.SystemAndNetworkEnabled &&
		(policy.SystemAndNetwork.SnapshotIntervalSeconds < 5 || policy.SystemAndNetwork.SnapshotIntervalSeconds > 3600) {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.ExternalDevicesEnabled && !policy.ExternalDevices.RecordConnect && !policy.ExternalDevices.RecordDisconnect {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.ExternalDevicesEnabled &&
		(policy.ExternalDevices.SnapshotIntervalSeconds < 1 || policy.ExternalDevices.SnapshotIntervalSeconds > 300) {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.UserSessionActivityEnabled && !policy.UserSession.anyEnabled() {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.UserSessionActivityEnabled &&
		(policy.UserSession.SampleIntervalSeconds < 1 || policy.UserSession.SampleIntervalSeconds > 60 ||
			policy.UserSession.ReportIntervalSeconds < 5 || policy.UserSession.ReportIntervalSeconds > 600 ||
			policy.UserSession.ReportIntervalSeconds < policy.UserSession.SampleIntervalSeconds) {
		return ErrMonitoringPolicyDetailInvalid
	}
	if policy.InputShieldEnabled && !policy.InputShield.valid() {
		return ErrMonitoringPolicyDetailInvalid
	}
	return nil
}

func (policy InputShieldPolicy) valid() bool {
	if !policy.BlockPhysicalKeyboard && !policy.BlockPhysicalMouse {
		return false
	}
	if policy.BlockPointerMovement && !policy.BlockPhysicalMouse {
		return false
	}
	if !policy.TrackActiveDevices && (policy.WarnOnDeviceArrival || policy.RecordDeviceRemoval) {
		return false
	}
	if policy.RecordKeyNames && (!policy.BlockPhysicalKeyboard || !policy.RecordBlockedInputCategory) {
		return false
	}
	if policy.RecordPointerCoordinates && (!policy.BlockPhysicalMouse || !policy.RecordBlockedInputCategory) {
		return false
	}
	switch policy.InjectedInputMode {
	case InjectedInputModeCompatible, InjectedInputModeRestricted, InjectedInputModeStrict:
	default:
		return false
	}
	if policy.ShowWarningOverlay && (policy.WarningDurationSeconds < 1 || policy.WarningDurationSeconds > 60 ||
		strings.TrimSpace(policy.WarningMessage) == "" || utf8.RuneCountInString(policy.WarningMessage) > 120) {
		return false
	}
	if policy.UnlockKeyCode < 1 || policy.UnlockKeyCode > 255 {
		return false
	}
	switch policy.UnlockTrigger {
	case InputShieldUnlockTriggerCombination:
		if !policy.UnlockRequireControl && !policy.UnlockRequireAlt && !policy.UnlockRequireShift {
			return false
		}
	case InputShieldUnlockTriggerTap:
		if policy.UnlockTapCount < 3 || policy.UnlockTapCount > 12 ||
			policy.UnlockTapWindowMilliseconds < 500 || policy.UnlockTapWindowMilliseconds > 10000 {
			return false
		}
	default:
		return false
	}
	switch policy.CredentialMode {
	case InputShieldCredentialWindows:
		if policy.AllowRecoveryCode {
			return false
		}
	case InputShieldCredentialLocal:
	default:
		return false
	}
	if policy.UnlockAction != InputShieldUnlockActionSuspend && policy.UnlockAction != InputShieldUnlockActionEndSession {
		return false
	}
	if policy.MaxFailedUnlockAttempts < 1 || policy.MaxFailedUnlockAttempts > 10 ||
		policy.UnlockLockoutSeconds < 10 || policy.UnlockLockoutSeconds > 3600 ||
		policy.HookHeartbeatSeconds < 1 || policy.HookHeartbeatSeconds > 30 {
		return false
	}
	return true
}

func (policy SystemNetworkPolicy) anyEnabled() bool {
	return policy.MonitorAccounts || policy.MonitorNetwork || policy.MonitorProxy || policy.MonitorFirewall ||
		policy.MonitorRemoteDesktop || policy.MonitorAuditPolicy || policy.MonitorSecurityLog || policy.MonitorClock ||
		policy.MonitorServices || policy.MonitorDrivers || policy.MonitorScheduledTasks || policy.MonitorStartupItems
}

func (policy UserSessionPolicy) anyEnabled() bool {
	return policy.RecordForegroundApplication || policy.RecordWindowTitle || policy.RecordKeyboardActivity ||
		policy.RecordMouseClicks || policy.RecordMouseWheel || policy.RecordHighRiskShortcuts
}

func (policy MonitoringPolicy) MonitoringLevel() MonitoringLevel {
	if policy.StrictReadAuditEnabled {
		return MonitoringLevelStrict
	}
	return MonitoringLevelStandard
}
