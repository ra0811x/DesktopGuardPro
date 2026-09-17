package domain

import (
	"errors"
	"testing"
)

func TestDefaultMonitoringPolicyUsesSimplifiedAuditDefaults(t *testing.T) {
	policy := DefaultMonitoringPolicy()
	if policy.Version != CurrentMonitoringPolicyVersion {
		t.Fatalf("default policy version = %d, want %d", policy.Version, CurrentMonitoringPolicyVersion)
	}
	if policy.Mode != MonitoringModeStandard {
		t.Fatalf("default policy mode = %q, want %q", policy.Mode, MonitoringModeStandard)
	}
	if !policy.FileActivityEnabled || !policy.ProcessAndSoftwareEnabled ||
		!policy.SystemAndNetworkEnabled || !policy.ExternalDevicesEnabled ||
		policy.UserSessionActivityEnabled {
		t.Fatalf("default policy = %+v, want standard audit capabilities with user activity disabled", policy)
	}
	if policy.StrictReadAuditEnabled {
		t.Fatalf("default policy = %+v, strict read audit must be disabled", policy)
	}
	if policy.InputShieldEnabled {
		t.Fatalf("default policy = %+v, input shield must require explicit opt-in", policy)
	}
	if !policy.InputShield.BlockPhysicalKeyboard || !policy.InputShield.BlockPhysicalMouse ||
		policy.InputShield.InjectedInputMode != InjectedInputModeCompatible ||
		policy.InputShield.CredentialMode != InputShieldCredentialWindows ||
		policy.InputShield.UnlockKeyCode != 0x20 || !policy.InputShield.UnlockRequireControl ||
		!policy.InputShield.UnlockRequireAlt || policy.InputShield.UnlockRequireShift ||
		policy.InputShield.UnlockAction != InputShieldUnlockActionSuspend ||
		policy.InputShield.RecordKeyNames || policy.InputShield.RecordPointerCoordinates ||
		policy.InputShield.RestoreAfterRestart {
		t.Fatalf("default input shield policy = %+v", policy.InputShield)
	}
	if policy.MonitoringLevel() != MonitoringLevelStandard {
		t.Fatalf("default level = %q, want %q", policy.MonitoringLevel(), MonitoringLevelStandard)
	}
	if !policy.File.CaptureContentHash || policy.File.CaptureBaseline ||
		policy.ProcessAndSoftware.RecordProcessStart || policy.ProcessAndSoftware.RecordProcessStop ||
		policy.ProcessAndSoftware.DetectInstallers || policy.ProcessAndSoftware.DetectSoftwareRepair ||
		policy.ProcessAndSoftware.CaptureImageMetadata ||
		policy.ProcessAndSoftware.SnapshotIntervalSeconds != 300 ||
		policy.SystemAndNetwork.SnapshotIntervalSeconds != 30 ||
		policy.ExternalDevices.SnapshotIntervalSeconds != 5 ||
		policy.UserSession.SampleIntervalSeconds != 5 || policy.UserSession.ReportIntervalSeconds != 30 {
		t.Fatalf("default detailed policy = %+v", policy)
	}
}

func TestBuiltInMonitoringPoliciesHaveDistinctCoverage(t *testing.T) {
	relaxed, err := BuiltInMonitoringPolicy(MonitoringModeRelaxed)
	if err != nil {
		t.Fatal(err)
	}
	if !relaxed.FileActivityEnabled || !relaxed.ExternalDevicesEnabled || relaxed.ProcessAndSoftwareEnabled ||
		relaxed.SystemAndNetworkEnabled || relaxed.UserSessionActivityEnabled || relaxed.StrictReadAuditEnabled ||
		relaxed.File.CaptureBaseline || relaxed.File.CaptureContentHash || relaxed.InputShieldEnabled {
		t.Fatalf("relaxed policy = %+v", relaxed)
	}

	standard, err := BuiltInMonitoringPolicy(MonitoringModeStandard)
	if err != nil {
		t.Fatal(err)
	}
	if !standard.FileActivityEnabled || !standard.ProcessAndSoftwareEnabled || !standard.SystemAndNetworkEnabled ||
		!standard.ExternalDevicesEnabled || standard.UserSessionActivityEnabled || standard.StrictReadAuditEnabled ||
		standard.InputShieldEnabled {
		t.Fatalf("standard policy = %+v", standard)
	}

	strict, err := BuiltInMonitoringPolicy(MonitoringModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	if !strict.StrictReadAuditEnabled || strict.File.CaptureBaseline || !strict.File.CaptureContentHash ||
		strict.UserSessionActivityEnabled || strict.ProcessAndSoftware.SnapshotIntervalSeconds != 300 || strict.InputShieldEnabled {
		t.Fatalf("strict policy = %+v", strict)
	}
}

func TestMonitoringPolicyMigratesVersionTwoAwayFromRemovedModeFeatures(t *testing.T) {
	policy := DefaultMonitoringPolicy()
	policy.Version = 2
	policy.UserSessionActivityEnabled = true
	policy.File.CaptureBaseline = true
	policy.ProcessAndSoftware.RecordProcessStart = true
	policy.ProcessAndSoftware.RecordProcessStop = true
	policy.ProcessAndSoftware.DetectInstallers = true
	policy.ProcessAndSoftware.DetectSoftwareRepair = true
	policy.ProcessAndSoftware.CaptureImageMetadata = true
	policy.ProcessAndSoftware.SnapshotIntervalSeconds = 2
	policy.InputShieldEnabled = true

	resolved := policy.Resolved()
	if resolved.Version != CurrentMonitoringPolicyVersion || resolved.UserSessionActivityEnabled ||
		resolved.File.CaptureBaseline || resolved.ProcessAndSoftware.RecordProcessStart ||
		resolved.ProcessAndSoftware.RecordProcessStop || resolved.ProcessAndSoftware.DetectInstallers ||
		resolved.ProcessAndSoftware.DetectSoftwareRepair || resolved.ProcessAndSoftware.CaptureImageMetadata ||
		resolved.ProcessAndSoftware.SnapshotIntervalSeconds != 300 || resolved.InputShieldEnabled {
		t.Fatalf("resolved version two policy = %+v", resolved)
	}
	if err := resolved.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMonitoringProfilesDoNotExposeInputShieldAsModeCapability(t *testing.T) {
	profiles, err := DefaultMonitoringProfiles()
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := profiles.Policy(MonitoringModeCustom)
	custom.InputShieldEnabled = true

	profiles, err = profiles.WithPolicy(MonitoringModeCustom, custom)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := profiles.Policy(MonitoringModeCustom)
	if resolved.InputShieldEnabled {
		t.Fatalf("custom mode retained input shield: %+v", resolved)
	}
}

func TestMonitoringProfilesExposeEditableModePoliciesAndResetDefaults(t *testing.T) {
	profiles, err := DefaultMonitoringProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []MonitoringMode{
		MonitoringModeRelaxed,
		MonitoringModeStandard,
		MonitoringModeStrict,
		MonitoringModeCustom,
	} {
		policy, err := profiles.Policy(mode)
		if err != nil {
			t.Fatalf("Policy(%s) error = %v", mode, err)
		}
		if policy.Mode != mode {
			t.Fatalf("Policy(%s) mode = %s", mode, policy.Mode)
		}
	}

	relaxed, _ := profiles.Policy(MonitoringModeRelaxed)
	relaxed.SystemAndNetworkEnabled = true
	profiles, err = profiles.WithPolicy(MonitoringModeRelaxed, relaxed)
	if err != nil {
		t.Fatal(err)
	}
	adjusted, _ := profiles.Policy(MonitoringModeRelaxed)
	if !adjusted.SystemAndNetworkEnabled || adjusted.Mode != MonitoringModeRelaxed {
		t.Fatalf("adjusted relaxed profile = %+v", adjusted)
	}
	profiles, err = profiles.Reset(MonitoringModeRelaxed)
	if err != nil {
		t.Fatal(err)
	}
	reset, _ := profiles.Policy(MonitoringModeRelaxed)
	want, _ := BuiltInMonitoringPolicy(MonitoringModeRelaxed)
	if reset != want {
		t.Fatalf("reset relaxed profile = %+v, want %+v", reset, want)
	}
}

func TestMonitoringProfilesRejectInvalidModeAndPolicy(t *testing.T) {
	profiles, err := DefaultMonitoringProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.Policy("unknown"); !errors.Is(err, ErrMonitoringModeInvalid) {
		t.Fatalf("Policy(unknown) error = %v", err)
	}
	invalid, _ := profiles.Policy(MonitoringModeCustom)
	invalid.FileActivityEnabled = false
	invalid.ProcessAndSoftwareEnabled = false
	invalid.SystemAndNetworkEnabled = false
	invalid.ExternalDevicesEnabled = false
	invalid.UserSessionActivityEnabled = false
	invalid.InputShieldEnabled = false
	if _, err := profiles.WithPolicy(MonitoringModeCustom, invalid); !errors.Is(err, ErrMonitoringPolicyRequiresCapability) {
		t.Fatalf("WithPolicy(invalid) error = %v", err)
	}
}

func TestMonitoringPolicyResolvesCurrentVersionAsCustom(t *testing.T) {
	policy := DefaultMonitoringPolicy()
	policy.Mode = ""
	policy.FileActivityEnabled = false

	resolved := policy.Resolved()
	if resolved.Mode != MonitoringModeCustom || resolved.FileActivityEnabled {
		t.Fatalf("resolved existing policy = %+v", resolved)
	}
}

func TestMonitoringPolicyResolvesVersionOneWithInputShieldDisabled(t *testing.T) {
	versionOne := DefaultMonitoringPolicy()
	versionOne.Version = 1
	versionOne.InputShieldEnabled = true
	versionOne.InputShield = InputShieldPolicy{}

	resolved := versionOne.Resolved()
	if resolved.Version != CurrentMonitoringPolicyVersion || resolved.InputShieldEnabled {
		t.Fatalf("resolved version one policy = %+v", resolved)
	}
	if !resolved.InputShield.BlockPhysicalKeyboard || resolved.InputShield.HookHeartbeatSeconds != 5 {
		t.Fatalf("resolved input shield policy = %+v", resolved.InputShield)
	}
	if err := resolved.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMonitoringPolicyAllowsInputShieldAsOnlyCapability(t *testing.T) {
	policy := DefaultMonitoringPolicy()
	policy.FileActivityEnabled = false
	policy.ProcessAndSoftwareEnabled = false
	policy.SystemAndNetworkEnabled = false
	policy.ExternalDevicesEnabled = false
	policy.UserSessionActivityEnabled = false
	policy.InputShieldEnabled = true
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMonitoringPolicyRejectsInvalidInputShieldSettings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*InputShieldPolicy)
	}{
		{name: "no blocked input", change: func(policy *InputShieldPolicy) {
			policy.BlockPhysicalKeyboard = false
			policy.BlockPhysicalMouse = false
		}},
		{name: "pointer movement without mouse", change: func(policy *InputShieldPolicy) {
			policy.BlockPhysicalMouse = false
		}},
		{name: "injected input mode", change: func(policy *InputShieldPolicy) {
			policy.InjectedInputMode = "unknown"
		}},
		{name: "empty warning", change: func(policy *InputShieldPolicy) {
			policy.WarningMessage = " "
		}},
		{name: "combination without modifier", change: func(policy *InputShieldPolicy) {
			policy.UnlockRequireControl = false
			policy.UnlockRequireAlt = false
			policy.UnlockRequireShift = false
		}},
		{name: "tap count", change: func(policy *InputShieldPolicy) {
			policy.UnlockTrigger = InputShieldUnlockTriggerTap
			policy.UnlockTapCount = 2
		}},
		{name: "windows recovery code", change: func(policy *InputShieldPolicy) {
			policy.AllowRecoveryCode = true
		}},
		{name: "unlock action", change: func(policy *InputShieldPolicy) {
			policy.UnlockAction = "unknown"
		}},
		{name: "heartbeat", change: func(policy *InputShieldPolicy) {
			policy.HookHeartbeatSeconds = 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultMonitoringPolicy()
			policy.InputShieldEnabled = true
			test.change(&policy.InputShield)
			if err := policy.Validate(); !errors.Is(err, ErrMonitoringPolicyDetailInvalid) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrMonitoringPolicyDetailInvalid)
			}
		})
	}
}

func TestMonitoringPolicyStrictReadAuditRequiresFileActivity(t *testing.T) {
	policy := DefaultMonitoringPolicy()
	policy.FileActivityEnabled = false
	policy.StrictReadAuditEnabled = true
	if err := policy.Validate(); !errors.Is(err, ErrStrictReadAuditRequiresFileActivity) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrStrictReadAuditRequiresFileActivity)
	}

	policy.FileActivityEnabled = true
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if policy.MonitoringLevel() != MonitoringLevelStrict {
		t.Fatalf("strict policy level = %q, want %q", policy.MonitoringLevel(), MonitoringLevelStrict)
	}
}

func TestMonitoringPolicyRequiresAtLeastOneCapability(t *testing.T) {
	if err := (MonitoringPolicy{}).Validate(); !errors.Is(err, ErrMonitoringPolicyRequiresCapability) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrMonitoringPolicyRequiresCapability)
	}
}

func TestMonitoringPolicyResolvesLegacyDetailsWithoutChangingCapabilities(t *testing.T) {
	legacy := MonitoringPolicy{FileActivityEnabled: true, UserSessionActivityEnabled: true}
	resolved := legacy.Resolved()
	if resolved.Version != CurrentMonitoringPolicyVersion || !resolved.FileActivityEnabled ||
		!resolved.UserSessionActivityEnabled || resolved.ProcessAndSoftwareEnabled ||
		!resolved.File.RecordModify || !resolved.UserSession.RecordForegroundApplication {
		t.Fatalf("resolved legacy policy = %+v", resolved)
	}
	if err := resolved.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMonitoringPolicyRejectsInvalidDetailedSettings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*MonitoringPolicy)
	}{
		{name: "unsupported version", change: func(policy *MonitoringPolicy) { policy.Version++ }},
		{name: "process interval", change: func(policy *MonitoringPolicy) { policy.ProcessAndSoftware.SnapshotIntervalSeconds = 0 }},
		{name: "system interval", change: func(policy *MonitoringPolicy) { policy.SystemAndNetwork.SnapshotIntervalSeconds = 4 }},
		{name: "device events", change: func(policy *MonitoringPolicy) {
			policy.ExternalDevices.RecordConnect = false
			policy.ExternalDevices.RecordDisconnect = false
		}},
		{name: "user report interval", change: func(policy *MonitoringPolicy) {
			policy.UserSessionActivityEnabled = true
			policy.UserSession.SampleIntervalSeconds = 20
			policy.UserSession.ReportIntervalSeconds = 10
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultMonitoringPolicy()
			test.change(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatalf("Validate() accepted invalid policy: %+v", policy)
			}
		})
	}
}

func TestMonitoringPolicyRejectsDependentDetailsWithoutTheirSource(t *testing.T) {
	tests := []struct {
		name   string
		change func(*MonitoringPolicy)
	}{
		{name: "device arrival warning without device tracking", change: func(policy *MonitoringPolicy) {
			policy.InputShieldEnabled = true
			policy.InputShield.TrackActiveDevices = false
		}},
		{name: "key names without blocked category recording", change: func(policy *MonitoringPolicy) {
			policy.InputShieldEnabled = true
			policy.InputShield.RecordKeyNames = true
			policy.InputShield.RecordBlockedInputCategory = false
		}},
		{name: "pointer coordinates without mouse blocking", change: func(policy *MonitoringPolicy) {
			policy.InputShieldEnabled = true
			policy.InputShield.RecordPointerCoordinates = true
			policy.InputShield.BlockPhysicalMouse = false
			policy.InputShield.BlockPointerMovement = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultMonitoringPolicy()
			test.change(&policy)
			if err := policy.Validate(); !errors.Is(err, ErrMonitoringPolicyDetailInvalid) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrMonitoringPolicyDetailInvalid)
			}
		})
	}
}
