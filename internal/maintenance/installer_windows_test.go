package maintenance

import (
	"errors"
	"testing"
	"time"

	"desktopguardpro/internal/installpolicy"
)

func TestInstallWindowsRunsSecurityStepsBeforeStartingService(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)

	result, err := installWindows(InstallOptions{}, dependencies)
	if err != nil {
		t.Fatalf("installWindows() error = %v", err)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data", "current-policy", "ensure-policy", "save-manifest", "register-startup", "start", "health"}
	assertCalls(t, calls, want)
	if !result.ServiceCreated || result.Options.ServiceName != DefaultServiceName || result.Preflight.WindowsBuild != windows10Build22H2 || result.OwnerUserSID == "" || result.Manifest.SchemaVersion != installManifestVersion {
		t.Fatalf("install result = %#v", result)
	}
}

func TestInstallWindowsUsesSpecifiedOwnerSID(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	validate := dependencies.validate
	dependencies.validate = func(options InstallOptions) (ValidatedInstallOptions, error) {
		validated, err := validate(options)
		validated.OwnerUserSID = options.OwnerUserSID
		return validated, err
	}
	var ensured installpolicy.Policy
	dependencies.ensurePolicy = func(_ string, policy installpolicy.Policy) error {
		calls = append(calls, "ensure-policy")
		ensured = policy
		return nil
	}

	result, err := installWindows(InstallOptions{OwnerUserSID: "S-1-5-21-1000-1001-1002-1004"}, dependencies)
	if err != nil {
		t.Fatalf("installWindows() error = %v", err)
	}
	if ensured.OwnerUserSID != "S-1-5-21-1000-1001-1002-1004" || result.OwnerUserSID != ensured.OwnerUserSID {
		t.Fatalf("owner policy = %#v, result = %#v", ensured, result)
	}
}

func TestInstallWindowsMigratesLegacySystemOwnerWhenExplicitlyAllowed(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	validate := dependencies.validate
	dependencies.validate = func(options InstallOptions) (ValidatedInstallOptions, error) {
		validated, err := validate(options)
		validated.OwnerUserSID = options.OwnerUserSID
		validated.AllowLegacySystemOwnerMigration = options.AllowLegacySystemOwnerMigration
		return validated, err
	}
	dependencies.ensurePolicy = func(string, installpolicy.Policy) error {
		calls = append(calls, "ensure-policy")
		if countCalls(calls, "ensure-policy") == 1 {
			return installpolicy.ErrPolicyOwnerMismatch
		}
		return nil
	}
	dependencies.migrateLegacySystemOwner = func(_ string, policy installpolicy.Policy) error {
		calls = append(calls, "migrate-legacy-system-owner")
		if policy.OwnerUserSID != "S-1-5-21-1000-1001-1002-1004" {
			t.Fatalf("migration owner = %q", policy.OwnerUserSID)
		}
		return nil
	}

	_, err := installWindows(InstallOptions{
		OwnerUserSID:                    "S-1-5-21-1000-1001-1002-1004",
		AllowLegacySystemOwnerMigration: true,
	}, dependencies)
	if err != nil {
		t.Fatalf("installWindows() error = %v", err)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data", "current-policy", "ensure-policy", "migrate-legacy-system-owner", "ensure-policy", "save-manifest", "register-startup", "start", "health"}
	assertCalls(t, calls, want)
}

func TestInstallWindowsSkipsHealthCheckOnlyWhenExplicitlyRequested(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	validate := dependencies.validate
	dependencies.validate = func(options InstallOptions) (ValidatedInstallOptions, error) {
		validated, err := validate(options)
		validated.SkipServiceHealthCheck = options.SkipServiceHealthCheck
		return validated, err
	}

	_, err := installWindows(InstallOptions{SkipServiceHealthCheck: true}, dependencies)
	if err != nil {
		t.Fatalf("installWindows() error = %v", err)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data", "current-policy", "ensure-policy", "save-manifest", "register-startup", "start"}
	assertCalls(t, calls, want)
}

func TestMigrateLegacySystemOwnerRejectsOtherExistingOwners(t *testing.T) {
	directory := t.TempDir()
	existing, err := installpolicy.New("S-1-5-21-1000-1001-1002-1003")
	if err != nil {
		t.Fatal(err)
	}
	if err := installpolicy.Save(directory, existing); err != nil {
		t.Fatal(err)
	}
	desired, err := installpolicy.New("S-1-5-21-1000-1001-1002-1004")
	if err != nil {
		t.Fatal(err)
	}

	err = migrateLegacySystemOwner(directory, desired)
	if !errors.Is(err, installpolicy.ErrPolicyOwnerMismatch) {
		t.Fatalf("migrateLegacySystemOwner() error = %v, want %v", err, installpolicy.ErrPolicyOwnerMismatch)
	}
	loaded, err := installpolicy.Load(directory)
	if err != nil || loaded != existing {
		t.Fatalf("Load() = %#v, %v; want %#v", loaded, err, existing)
	}
}

func TestMigrateLegacySystemOwnerReplacesOnlySystemPolicy(t *testing.T) {
	directory := t.TempDir()
	legacy, err := installpolicy.New(legacySystemOwnerSID)
	if err != nil {
		t.Fatal(err)
	}
	if err := installpolicy.Save(directory, legacy); err != nil {
		t.Fatal(err)
	}
	desired, err := installpolicy.New("S-1-5-21-1000-1001-1002-1004")
	if err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacySystemOwner(directory, desired); err != nil {
		t.Fatalf("migrateLegacySystemOwner() error = %v", err)
	}
	loaded, err := installpolicy.Load(directory)
	if err != nil || loaded != desired {
		t.Fatalf("Load() = %#v, %v; want %#v", loaded, err, desired)
	}
}

func TestInstallWindowsRollsBackFreshServiceAfterStartFailure(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	wantErr := errors.New("service failed to start")
	dependencies.startService = func(string) error {
		calls = append(calls, "start")
		return wantErr
	}

	_, err := installWindows(InstallOptions{}, dependencies)
	if !errors.Is(err, wantErr) {
		t.Fatalf("installWindows() error = %v, want %v", err, wantErr)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data", "current-policy", "ensure-policy", "save-manifest", "register-startup", "start", "unregister-startup", "delete-service"}
	assertCalls(t, calls, want)
}

func TestInstallWindowsRollsBackFreshServiceAfterHealthFailure(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	wantErr := errors.New("pipe unavailable")
	dependencies.waitForHealth = func() error {
		calls = append(calls, "health")
		return wantErr
	}

	_, err := installWindows(InstallOptions{}, dependencies)
	if !errors.Is(err, wantErr) {
		t.Fatalf("installWindows() error = %v, want %v", err, wantErr)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data", "current-policy", "ensure-policy", "save-manifest", "register-startup", "start", "health", "unregister-startup", "delete-service"}
	assertCalls(t, calls, want)
}

func TestInstallWindowsRollsBackFreshServiceAfterOwnerPolicyFailure(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	wantErr := installpolicy.ErrPolicyOwnerMismatch
	dependencies.ensurePolicy = func(string, installpolicy.Policy) error {
		calls = append(calls, "ensure-policy")
		return wantErr
	}

	_, err := installWindows(InstallOptions{}, dependencies)
	if !errors.Is(err, wantErr) {
		t.Fatalf("installWindows() error = %v, want %v", err, wantErr)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data", "current-policy", "ensure-policy", "delete-service"}
	assertCalls(t, calls, want)
}

func TestInstallWindowsDoesNotDeleteExistingServiceAfterUpgradeFailure(t *testing.T) {
	var calls []string
	dependencies := successfulInstallerDependencies(&calls)
	dependencies.configureService = func(ValidatedInstallOptions) (bool, error) {
		calls = append(calls, "configure")
		return false, nil
	}
	wantErr := errors.New("ACL failure")
	dependencies.secureData = func(string, string) error {
		calls = append(calls, "secure-data")
		return wantErr
	}

	_, err := installWindows(InstallOptions{}, dependencies)
	if !errors.Is(err, wantErr) {
		t.Fatalf("installWindows() error = %v, want %v", err, wantErr)
	}
	want := []string{"validate", "preflight", "build-manifest", "configure", "secure-data"}
	assertCalls(t, calls, want)
}

func successfulInstallerDependencies(calls *[]string) installerDependencies {
	validated := ValidatedInstallOptions{
		ServiceName:       DefaultServiceName,
		ServiceExecutable: `C:\Desktop Guard Pro\desktop-guard-service.exe`,
		AgentExecutable:   `C:\Desktop Guard Pro\desktop-guard-agent.exe`,
		UIExecutable:      `C:\Desktop Guard Pro\desktop-guard-ui.exe`,
		DataDirectory:     `C:\ProgramData\Desktop Guard Pro`,
		Version:           "1.0.0",
	}
	return installerDependencies{
		validate: func(InstallOptions) (ValidatedInstallOptions, error) {
			*calls = append(*calls, "validate")
			return validated, nil
		},
		preflight: func(ValidatedInstallOptions) (PreflightReport, error) {
			*calls = append(*calls, "preflight")
			return PreflightReport{WindowsBuild: windows10Build22H2}, nil
		},
		buildManifest: func(ValidatedInstallOptions, PreflightReport, time.Time) (InstallManifest, error) {
			*calls = append(*calls, "build-manifest")
			return InstallManifest{SchemaVersion: installManifestVersion}, nil
		},
		configureService: func(ValidatedInstallOptions) (bool, error) {
			*calls = append(*calls, "configure")
			return true, nil
		},
		secureData: func(string, string) error {
			*calls = append(*calls, "secure-data")
			return nil
		},
		currentPolicy: func() (installpolicy.Policy, error) {
			*calls = append(*calls, "current-policy")
			return installpolicy.New("S-1-5-21-1000-1001-1002-1003")
		},
		ensurePolicy: func(string, installpolicy.Policy) error {
			*calls = append(*calls, "ensure-policy")
			return nil
		},
		saveManifest: func(string, InstallManifest) error {
			*calls = append(*calls, "save-manifest")
			return nil
		},
		registerStartup: func(string) error {
			*calls = append(*calls, "register-startup")
			return nil
		},
		startService: func(string) error {
			*calls = append(*calls, "start")
			return nil
		},
		waitForHealth: func() error {
			*calls = append(*calls, "health")
			return nil
		},
		deleteService: func(string) error {
			*calls = append(*calls, "delete-service")
			return nil
		},
		unregisterStartup: func() error {
			*calls = append(*calls, "unregister-startup")
			return nil
		},
		now: time.Now,
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("calls = %#v, want %#v", got, want)
		}
	}
}

func countCalls(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}
