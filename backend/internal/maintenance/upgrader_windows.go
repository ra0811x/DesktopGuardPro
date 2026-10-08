package maintenance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/installpolicy"
	"desktopguardpro/internal/maintenancegate"
	platformwindows "desktopguardpro/internal/windows"
)

var ErrUpgradePublisherMismatch = errors.New("upgrade publisher does not match installed publisher")

type UpgradeResult struct {
	PreviousVersion string    `json:"previousVersion"`
	CurrentVersion  string    `json:"currentVersion"`
	CompletedUTC    time.Time `json:"completedUtc"`
}

type componentSwapTransaction interface {
	Activate() error
	Rollback() error
	Commit() error
}

type upgraderDependencies struct {
	backupData           func(string) (func() error, error)
	beginMaintenance     func(string) (func() error, error)
	validate             func(UpgradeOptions) (ValidatedUpgradeOptions, error)
	loadManifest         func(string) (InstallManifest, error)
	verifyInstalled      func(InstallManifest) error
	loadPolicy           func(string) (installpolicy.Policy, error)
	currentPolicy        func() (installpolicy.Policy, error)
	checkNoActiveSession func() error
	preflight            func(ValidatedInstallOptions) (PreflightReport, error)
	prepareSwap          func([]ComponentReplacement) (componentSwapTransaction, error)
	runtimeReplacements  func(ValidatedUpgradeOptions, InstallManifest) ([]ComponentReplacement, error)
	unregisterStartup    func() error
	registerStartup      func(string) error
	stopService          func(string) error
	startService         func(string) error
	waitForHealth        func() error
	buildManifest        func(ValidatedInstallOptions, PreflightReport, time.Time) (InstallManifest, error)
	saveManifest         func(string, InstallManifest) error
	now                  func() time.Time
}

func UpgradeWindows(options UpgradeOptions) (UpgradeResult, error) {
	if err := validateRuntimeConfiguration(options.DataDirectory, options.ServiceName); err != nil {
		return UpgradeResult{}, err
	}
	caller, err := os.Executable()
	if err != nil {
		return UpgradeResult{}, err
	}
	if err := validateUpgradeCaller(caller, options.InstallDirectory); err != nil {
		return UpgradeResult{}, err
	}
	dependencies := upgraderDependencies{
		backupData:       backupUpgradeData,
		beginMaintenance: acquireMaintenanceWindow,
		validate:         ValidateUpgradeOptions, loadManifest: LoadInstallManifest,
		verifyInstalled: VerifyInstalledComponents, loadPolicy: installpolicy.Load,
		currentPolicy: func() (installpolicy.Policy, error) { return maintenanceCallerPolicy("") }, checkNoActiveSession: func() error { return checkPersistedProtectionState(options.DataDirectory) },
		preflight: RunWindowsInstallPreflight,
		prepareSwap: func(replacements []ComponentReplacement) (componentSwapTransaction, error) {
			return PrepareComponentSwap(replacements)
		},
		runtimeReplacements: prepareRuntimeReplacements,
		unregisterStartup:   UnregisterAgentStartup, registerStartup: RegisterAgentStartup,
		stopService: StopWindowsService, startService: StartWindowsService, waitForHealth: WaitForWindowsServiceHealth,
		buildManifest: BuildInstallManifest, saveManifest: SaveInstallManifest, now: time.Now,
	}
	return upgradeWindows(options, dependencies)
}

func validateUpgradeCaller(caller, installDirectory string) error {
	if samePath(caller, filepath.Join(installDirectory, "desktop-guard-maintenance.exe")) {
		return fmt.Errorf("%w: run desktop-guard-maintenance.exe from the staged release directory so the installed maintenance component can be replaced", ErrUpgradeOptionsInvalid)
	}
	return nil
}

func upgradeWindows(options UpgradeOptions, dependencies upgraderDependencies) (UpgradeResult, error) {
	validated, err := dependencies.validate(options)
	if err != nil {
		return UpgradeResult{}, err
	}
	installedManifest, err := dependencies.loadManifest(validated.Current.DataDirectory)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("load installed manifest: %w", err)
	}
	if installedManifest.ProductVersion != validated.Current.Version {
		return UpgradeResult{}, ErrUpgradeOptionsInvalid
	}
	if err := dependencies.verifyInstalled(installedManifest); err != nil {
		return UpgradeResult{}, fmt.Errorf("verify installed components: %w", err)
	}
	installedPolicy, err := dependencies.loadPolicy(validated.Current.DataDirectory)
	if err != nil {
		return UpgradeResult{}, err
	}
	currentPolicy, err := dependencies.currentPolicy()
	if err != nil || !strings.EqualFold(installedPolicy.OwnerUserSID, currentPolicy.OwnerUserSID) {
		return UpgradeResult{}, installpolicy.ErrPolicyOwnerMismatch
	}
	if dependencies.beginMaintenance != nil {
		release, err := dependencies.beginMaintenance(validated.Current.DataDirectory)
		if err != nil {
			return UpgradeResult{}, err
		}
		defer release()
	}
	if err := dependencies.checkNoActiveSession(); err != nil {
		return UpgradeResult{}, err
	}
	preflight, err := dependencies.preflight(validated.Staged)
	if err != nil {
		return UpgradeResult{}, err
	}
	if !strings.EqualFold(installedManifest.SignerSHA256, preflight.SignerSHA256) {
		return UpgradeResult{}, ErrUpgradePublisherMismatch
	}
	replacements := []ComponentReplacement{
		{Name: "service", TargetPath: validated.Current.ServiceExecutable, StagedPath: validated.Staged.ServiceExecutable},
		{Name: "ui", TargetPath: validated.Current.UIExecutable, StagedPath: validated.Staged.UIExecutable},
		{Name: "agent", TargetPath: validated.Current.AgentExecutable, StagedPath: validated.Staged.AgentExecutable},
		{Name: "maintenance", TargetPath: validated.Current.maintenanceExecutable(), StagedPath: validated.Staged.maintenanceExecutable()},
	}
	if dependencies.runtimeReplacements != nil {
		runtimePairs, err := dependencies.runtimeReplacements(validated, installedManifest)
		if err != nil {
			return UpgradeResult{}, err
		}
		replacements = append(replacements, runtimePairs...)
	}
	swap, err := dependencies.prepareSwap(replacements)
	if err != nil {
		return UpgradeResult{}, err
	}
	startupRemoved := false
	manifestWriteAttempted := false
	var restoreData func() error
	rollback := func(cause error, activated bool) error {
		var rollbackErrors []error
		if activated {
			if err := dependencies.stopService(validated.Current.ServiceName); err != nil {
				return errors.Join(cause, err)
			}
			if err := swap.Rollback(); err != nil {
				return errors.Join(cause, err)
			}
			if restoreData != nil {
				if err := restoreData(); err != nil {
					return errors.Join(cause, err)
				}
			}
		} else if err := swap.Rollback(); err != nil {
			return errors.Join(cause, err)
		}
		if manifestWriteAttempted {
			rollbackErrors = append(rollbackErrors, dependencies.saveManifest(validated.Current.DataDirectory, installedManifest))
		}
		if startupRemoved {
			rollbackErrors = append(rollbackErrors, dependencies.registerStartup(validated.Current.AgentExecutable))
		}
		rollbackErrors = append(rollbackErrors, dependencies.startService(validated.Current.ServiceName), dependencies.waitForHealth())
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}
	if err := dependencies.unregisterStartup(); err != nil {
		return UpgradeResult{}, errors.Join(err, swap.Rollback())
	}
	startupRemoved = true
	if err := dependencies.stopService(validated.Current.ServiceName); err != nil {
		return UpgradeResult{}, rollback(err, false)
	}
	if dependencies.backupData != nil {
		restoreData, err = dependencies.backupData(validated.Current.DataDirectory)
		if err != nil {
			return UpgradeResult{}, rollback(err, false)
		}
	}
	if err := swap.Activate(); err != nil {
		return UpgradeResult{}, rollback(err, false)
	}
	if err := dependencies.startService(validated.Current.ServiceName); err != nil {
		return UpgradeResult{}, rollback(err, true)
	}
	if err := dependencies.waitForHealth(); err != nil {
		return UpgradeResult{}, rollback(err, true)
	}
	targetOptions := validated.Current
	targetOptions.Version = validated.Staged.Version
	newManifest, err := dependencies.buildManifest(targetOptions, preflight, dependencies.now().UTC())
	if err != nil {
		return UpgradeResult{}, rollback(err, true)
	}
	manifestWriteAttempted = true
	if err := dependencies.saveManifest(validated.Current.DataDirectory, newManifest); err != nil {
		return UpgradeResult{}, rollback(err, true)
	}
	if err := dependencies.registerStartup(validated.Current.AgentExecutable); err != nil {
		return UpgradeResult{}, rollback(err, true)
	}
	startupRemoved = false
	if err := swap.Commit(); err != nil {
		return UpgradeResult{}, fmt.Errorf("clean upgrade backup: %w", err)
	}
	return UpgradeResult{
		PreviousVersion: validated.Current.Version, CurrentVersion: validated.Staged.Version,
		CompletedUTC: dependencies.now().UTC(),
	}, nil
}

func prepareRuntimeReplacements(options ValidatedUpgradeOptions, installed InstallManifest) ([]ComponentReplacement, error) {
	staged, err := readReleaseRuntimeFiles(options.Staged.InstallDirectory, true)
	if err != nil {
		return nil, err
	}
	pairs := make([]ComponentReplacement, 0, len(staged)+len(installed.RuntimeFiles)+1)
	names := make(map[string]bool)
	for _, file := range staged {
		target, err := runtimeFilePath(options.Current.InstallDirectory, file.Path)
		if err != nil {
			return nil, err
		}
		source, err := runtimeFilePath(options.Staged.InstallDirectory, file.Path)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, ComponentReplacement{Name: file.Path, TargetPath: target, StagedPath: source, AllowCreate: true, SHA256: file.SHA256})
		names[strings.ToLower(file.Path)] = true
	}
	// Only remove files owned by the old manifest. Legacy manifests without an
	// inventory can still upgrade, but their unrecorded extra files are preserved.
	for _, file := range installed.RuntimeFiles {
		if names[strings.ToLower(file.Path)] {
			continue
		}
		target, err := runtimeFilePath(options.Current.InstallDirectory, file.Path)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, ComponentReplacement{Name: file.Path, TargetPath: target, Remove: true})
	}
	manifestPath, err := runtimeFilePath(options.Staged.InstallDirectory, "release-manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(manifestPath); err == nil {
		record, err := inspectInstallComponent("release-manifest", manifestPath)
		if err != nil {
			return nil, err
		}
		target, err := runtimeFilePath(options.Current.InstallDirectory, "release-manifest.json")
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, ComponentReplacement{Name: "release-manifest", TargetPath: target, StagedPath: manifestPath, AllowCreate: true, SHA256: record.SHA256})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return pairs, nil
}

func acquireMaintenanceWindow(dataDirectory string) (func() error, error) {
	if err := platformwindows.ValidateMaintenanceDataDirectory(dataDirectory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gate, err := maintenancegate.Acquire(ctx, dataDirectory)
	if err != nil {
		return nil, err
	}
	if err := gate.CheckReady(); err != nil {
		gate.Close()
		return nil, err
	}
	if err := requireServiceMaintenanceGate(); err != nil {
		gate.Close()
		return nil, err
	}
	return gate.Close, nil
}
