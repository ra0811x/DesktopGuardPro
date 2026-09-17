package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/appbridge"
	"desktopguardpro/internal/installpolicy"
	coreservice "desktopguardpro/internal/service"
	platformwindows "desktopguardpro/internal/windows"
	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const msiFailureLogFileName = "msi-maintenance-error.json"

type msiFailureLog struct {
	Stage       string    `json:"stage"`
	Version     string    `json:"version"`
	ObservedUTC time.Time `json:"observedUtc"`
	Error       string    `json:"error"`
}

func MSIWindows(stage string, options MSIOptions) (result MSIResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			_ = recordMSIFailure(options.DataDirectory, stage, options.Version, resultErr)
		} else if stage == "commit" {
			_ = clearMSIFailureLog(options.DataDirectory)
		}
	}()
	if stage != "prepare" && stage != "apply" && stage != "stop-rollback" && stage != "rollback" && stage != "commit" {
		return MSIResult{}, ErrUpgradeOptionsInvalid
	}
	owner, err := maintenanceCallerPolicy(options.OwnerUserSID)
	if err != nil {
		return MSIResult{}, err
	}
	expected, err := expectedServiceDataDirectory()
	if err != nil || !samePath(options.DataDirectory, expected) {
		return MSIResult{}, ErrInstallPathInvalid
	}
	options.DataDirectory = expected
	options.InstallDirectory, err = normalizeAbsolutePath(options.InstallDirectory)
	if err != nil {
		return MSIResult{}, err
	}
	if err := (windowsPreflightProbe{}).TrustInstallDirectory(options.InstallDirectory); err != nil {
		return MSIResult{}, err
	}
	if !validNumericVersion(options.Version) || len(options.TransactionID) < 1 || len(options.TransactionID) > 80 || strings.Trim(options.TransactionID, "{}-0123456789abcdefABCDEF") != "" {
		return MSIResult{}, ErrUpgradeOptionsInvalid
	}
	self, err := os.Executable()
	if err != nil {
		return MSIResult{}, err
	}
	signer, err := VerifyAuthenticodeComponent(self)
	if err != nil {
		return MSIResult{}, fmt.Errorf("verify MSI maintenance binary: %w", err)
	}
	if _, err := os.Lstat(expected); os.IsNotExist(err) {
		if stage == "rollback" || stage == "stop-rollback" {
			return MSIResult{Stage: stage, TransactionID: options.TransactionID}, nil
		}
		if stage != "prepare" {
			return MSIResult{}, err
		}
		if err := platformwindows.SecureOwnerDirectory(expected, "S-1-5-18"); err != nil {
			return MSIResult{}, err
		}
	}
	if err := platformwindows.ValidateMaintenanceDataDirectory(expected); err != nil {
		return MSIResult{}, err
	}
	dependencies := msiDependencies{
		backup:   backupMSIData,
		ownerSID: owner.OwnerUserSID, signer: signer,
		snapshot: func() (msiSnapshot, error) { return snapshotMSIInstallation(options) },
		checkSession: func(before msiSnapshot) error {
			_, err := os.Lstat(filepath.Join(expected, "desktop-guard.db"))
			if os.IsNotExist(err) && len(before.PolicyJSON) == 0 && !before.ServiceExisted {
				return nil
			}
			return checkPersistedProtectionState(expected)
		},
		stop:    stopMSIService,
		apply:   applyMSIInstallation,
		restore: restoreMSIInstallation,
		finish:  finishMSIInstallation,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runMSITransaction(ctx, stage, options, dependencies)
}

func recordMSIFailure(dataDirectory, stage, version string, cause error) error {
	expected, err := expectedServiceDataDirectory()
	if err != nil || !samePath(dataDirectory, expected) {
		return ErrInstallPathInvalid
	}
	if err := platformwindows.ValidateMaintenanceDataDirectory(expected); err != nil {
		return err
	}
	return writeMSIFailureLog(filepath.Join(expected, msiFailureLogFileName), msiFailureLog{
		Stage: strings.TrimSpace(stage), Version: strings.TrimSpace(version),
		ObservedUTC: time.Now().UTC(), Error: strings.TrimSpace(cause.Error()),
	})
}

func writeMSIFailureLog(path string, entry msiFailureLog) error {
	encoded, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return writeMSIStateFile(path, encoded)
}

func clearMSIFailureLog(dataDirectory string) error {
	expected, err := expectedServiceDataDirectory()
	if err != nil || !samePath(dataDirectory, expected) {
		return ErrInstallPathInvalid
	}
	err = os.Remove(filepath.Join(expected, msiFailureLogFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func snapshotMSIInstallation(options MSIOptions) (msiSnapshot, error) {
	var before msiSnapshot
	policyBytes, policyErr := readOptionalMSIFile(filepath.Join(options.DataDirectory, installpolicy.FileName))
	manifestBytes, manifestErr := readOptionalMSIFile(filepath.Join(options.DataDirectory, InstallManifestFileName))
	if err := errors.Join(policyErr, manifestErr); err != nil {
		return before, err
	}
	if (len(policyBytes) == 0) != (len(manifestBytes) == 0) {
		return before, errors.New("incomplete installation metadata; recover it before MSI maintenance")
	}
	if len(policyBytes) != 0 {
		policy, err := installpolicy.Load(options.DataDirectory)
		if err != nil {
			return before, err
		}
		manifest, err := LoadInstallManifest(options.DataDirectory)
		if err != nil {
			return before, err
		}
		for _, component := range manifest.Components {
			if !samePath(component.Path, filepath.Join(options.InstallDirectory, "desktop-guard-"+component.Name+".exe")) {
				return before, ErrInstallLayoutInvalid
			}
		}
		before.OwnerSID, before.Manifest = policy.OwnerUserSID, manifest
		before.PolicyJSON, before.ManifestJSON = policyBytes, manifestBytes
	}
	manager, err := mgr.Connect()
	if err != nil {
		return before, err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(DefaultServiceName)
	if err != nil && !errors.Is(err, winapi.ERROR_SERVICE_DOES_NOT_EXIST) {
		return before, err
	}
	if err == nil {
		defer service.Close()
		if before.OwnerSID == "" {
			return before, errors.New("existing service has no trusted installation metadata")
		}
		config, err := service.Config()
		if err != nil {
			return before, err
		}
		if !samePath(strings.Trim(config.BinaryPathName, `"`), filepath.Join(options.InstallDirectory, "desktop-guard-service.exe")) || !strings.EqualFold(config.ServiceStartName, "LocalSystem") {
			return before, errors.New("existing service configuration does not match the installation")
		}
		status, err := waitForStableMSIServiceState(service.Query, 15*time.Second, 200*time.Millisecond)
		if err != nil {
			return before, err
		}
		if err := VerifyInstalledComponents(before.Manifest); err != nil {
			return before, err
		}
		if err := requireServiceMaintenanceGate(); err != nil {
			return before, err
		}
		before.ServiceExisted, before.ServiceRunning, before.ServiceConfig = true, status.State == svc.Running, &config
	}
	before.Startup, err = readMSIStartup()
	return before, err
}

func waitForStableMSIServiceState(
	query func() (svc.Status, error),
	timeout time.Duration,
	pollInterval time.Duration,
) (svc.Status, error) {
	if query == nil || timeout <= 0 || pollInterval <= 0 {
		return svc.Status{}, errors.New("invalid MSI service state wait configuration")
	}
	deadline := time.Now().Add(timeout)
	for {
		status, err := query()
		if err != nil {
			return svc.Status{}, err
		}
		switch status.State {
		case svc.Running, svc.Stopped:
			return status, nil
		case svc.StartPending, svc.StopPending:
		default:
			return svc.Status{}, fmt.Errorf("service entered unsupported maintenance state %d", status.State)
		}
		if !time.Now().Before(deadline) {
			return svc.Status{}, fmt.Errorf("service did not reach a stable state within %s", timeout)
		}
		time.Sleep(pollInterval)
	}
}

func msiInstallOptions(options MSIOptions, owner string) InstallOptions {
	return InstallOptions{
		InstallDirectory: options.InstallDirectory, DataDirectory: options.DataDirectory, OwnerUserSID: owner,
		Version: options.Version, ServiceName: DefaultServiceName,
		ServiceExecutable: filepath.Join(options.InstallDirectory, "desktop-guard-service.exe"),
		UIExecutable:      filepath.Join(options.InstallDirectory, "desktop-guard-ui.exe"),
		AgentExecutable:   filepath.Join(options.InstallDirectory, "desktop-guard-agent.exe"),
	}
}

func applyMSIInstallation(journal msiJournal) error {
	options := msiInstallOptions(journal.Options, journal.OwnerSID)
	validated, err := ValidateInstallOptions(options)
	if err != nil {
		return err
	}
	preflight, err := RunWindowsInstallPreflight(validated)
	if err != nil {
		return err
	}
	if !strings.EqualFold(preflight.SignerSHA256, journal.Signer.SHA256) {
		return ErrUpgradePublisherMismatch
	}
	if err := verifyMSIReleaseFiles(journal.Options.InstallDirectory, journal.Options.Version, journal.Signer.SHA256); err != nil {
		return err
	}
	if err := cancelScheduledProgramRemoval(validated.InstallDirectory); err != nil {
		return fmt.Errorf("cancel obsolete program removal: %w", err)
	}
	if !journal.Before.ServiceExisted {
		_, err := InstallWindows(options)
		return err
	}
	// Preserve the installed service settings. Only repair executable quoting;
	// the exact previous config remains available to rollback.
	if journal.Before.ServiceConfig == nil {
		return errors.New("missing original service configuration")
	}
	config := *journal.Before.ServiceConfig
	config.BinaryPathName = winapi.EscapeArg(validated.ServiceExecutable)
	if err := updateMSIServiceConfig(config); err != nil {
		return err
	}
	if err := StartWindowsService(DefaultServiceName); err != nil {
		return err
	}
	if err := WaitForWindowsServiceHealth(); err != nil {
		return err
	}
	manifest, err := BuildInstallManifest(validated, preflight, time.Now().UTC())
	if err != nil {
		return err
	}
	return SaveInstallManifest(validated.DataDirectory, manifest)
}

func finishMSIInstallation(journal msiJournal) error {
	// Retain the current protected recovery copy before trimming older copies.
	if err := saveMSIJournal(filepath.Join(msiBackupDirectory(journal), "recovery.json"), journal); err != nil {
		return err
	}
	manifest, err := LoadInstallManifest(journal.Options.DataDirectory)
	if err != nil {
		return err
	}
	if manifest.ProductVersion != journal.Options.Version || !strings.EqualFold(manifest.SignerSHA256, journal.Signer.SHA256) {
		return ErrInstallManifestInvalid
	}
	if err := VerifyInstalledComponents(manifest); err != nil {
		return err
	}
	if err := WaitForWindowsServiceHealth(); err != nil {
		return err
	}
	if journal.Before.ServiceExisted && !journal.Before.ServiceRunning {
		if err := StopWindowsService(DefaultServiceName); err != nil {
			return err
		}
	}
	if _, err := pruneMSIRecoveryBackups(journal.Options.DataDirectory, journal.BackupDirectoryName, maximumMSIRecoveryBackups); err != nil {
		return fmt.Errorf("prune MSI recovery backups: %w", err)
	}
	return nil
}

func restoreMSIInstallation(journal msiJournal) error {
	if err := restoreMSIBackup(journal); err != nil {
		return err
	}
	if journal.Before.ServiceExisted {
		// Windows Installer must have restored its files before this action.
		if err := VerifyInstalledComponents(journal.Before.Manifest); err != nil {
			return err
		}
		if journal.Before.ServiceConfig == nil {
			return errors.New("missing original service configuration")
		}
		if err := updateMSIServiceConfig(*journal.Before.ServiceConfig); err != nil {
			return err
		}
	} else if err := DeleteWindowsService(DefaultServiceName); err != nil {
		return err
	}
	if err := restoreMSIData(journal.Options.DataDirectory, journal.Before); err != nil {
		return err
	}
	if err := restoreMSIStartup(journal.Before.Startup); err != nil {
		return err
	}
	if journal.Before.ServiceRunning {
		if err := StartWindowsService(DefaultServiceName); err != nil {
			return err
		}
		// Earlier releases did not authorize LocalSystem health probes. Query
		// SCM for the restored release; new-release health is always probed above.
		return waitMSIServiceRunning()
	}
	return nil
}

func restoreMSIData(directory string, before msiSnapshot) error {
	for name, content := range map[string][]byte{installpolicy.FileName: before.PolicyJSON, InstallManifestFileName: before.ManifestJSON} {
		path := filepath.Join(directory, name)
		if len(content) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := writeMSIStateFile(path, content); err != nil {
			return err
		}
	}
	return nil
}

func readOptionalMSIFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 || info.Size() > maximumManifestSize {
		return nil, errors.New("invalid MSI state file")
	}
	return os.ReadFile(path)
}

func stopMSIService() error {
	err := StopWindowsService(DefaultServiceName)
	if errors.Is(err, winapi.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	return err
}

func updateMSIServiceConfig(config mgr.Config) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(DefaultServiceName)
	if err != nil {
		return err
	}
	defer service.Close()
	return service.UpdateConfig(config)
}

func waitMSIServiceRunning() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(DefaultServiceName)
	if err != nil {
		return err
	}
	defer service.Close()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("restored service did not reach running state")
}

func readMSIStartup() (msiStartupSnapshot, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, startupRegistryPath, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return msiStartupSnapshot{}, err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue(startupValueName)
	if errors.Is(err, registry.ErrNotExist) {
		return msiStartupSnapshot{}, nil
	}
	return msiStartupSnapshot{Present: true, Value: value, Expand: kind == registry.EXPAND_SZ}, err
}

func restoreMSIStartup(before msiStartupSnapshot) error {
	if !before.Present {
		return UnregisterAgentStartup()
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, startupRegistryPath, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return err
	}
	defer key.Close()
	if before.Expand {
		return key.SetExpandStringValue(startupValueName, before.Value)
	}
	return key.SetStringValue(startupValueName, before.Value)
}

func requireServiceMaintenanceGate() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(DefaultServiceName)
	if errors.Is(err, winapi.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if status.State != svc.Running {
		return errors.New("wait for a stable service state before maintenance")
	}
	health, probeErr := appbridge.New().GetHealth()
	return validateMaintenanceCapability(true, health, probeErr)
}

func validateMaintenanceCapability(running bool, health coreservice.HealthResult, probeErr error) error {
	if !running {
		return nil
	}
	if probeErr != nil || !health.MaintenanceGate {
		return fmt.Errorf("the running release cannot freeze new sessions; finish protection, stop the legacy service, and retry maintenance: %v", probeErr)
	}
	return nil
}
