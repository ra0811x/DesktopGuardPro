package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/appbridge"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/installpolicy"
	coreservice "desktopguardpro/internal/service"

	winapi "golang.org/x/sys/windows"
)

var ErrActiveProtectionSession = errors.New("an active protection session blocks uninstall")

type UninstallResult struct {
	Succeeded       bool            `json:"succeeded"`
	Stage           string          `json:"stage,omitempty"`
	Failure         string          `json:"failure,omitempty"`
	CompletedUTC    time.Time       `json:"completedUtc"`
	Disposition     DataDisposition `json:"disposition"`
	DataPreserved   bool            `json:"dataPreserved"`
	ArchivePath     string          `json:"archivePath,omitempty"`
	LogPath         string          `json:"logPath,omitempty"`
	RestartRequired bool            `json:"restartRequired"`
}

type uninstallerDependencies struct {
	beginMaintenance       func(string) (func() error, error)
	expectedDataDirectory  func() (string, error)
	validate               func(UninstallOptions, string) (ValidatedUninstallOptions, error)
	loadPolicy             func(string) (installpolicy.Policy, error)
	loadManifest           func(string) (InstallManifest, error)
	currentPolicy          func() (installpolicy.Policy, error)
	checkNoActiveSession   func() error
	restoreSystemChanges   func(string) error
	unregisterStartup      func() error
	deleteService          func(string) error
	scheduleProgramRemoval func(InstallManifest) error
	removeDataDirectory    func(string, string) error
	writeLog               func(UninstallResult, string) (string, error)
	now                    func() time.Time
}

func UninstallWindows(options UninstallOptions) (UninstallResult, error) {
	if err := validateRuntimeConfiguration(options.DataDirectory, options.ServiceName); err != nil {
		return UninstallResult{}, err
	}
	dependencies := uninstallerDependencies{
		beginMaintenance:       acquireMaintenanceWindow,
		expectedDataDirectory:  expectedServiceDataDirectory,
		validate:               ValidateUninstallOptions,
		loadPolicy:             installpolicy.Load,
		loadManifest:           LoadInstallManifest,
		currentPolicy:          func() (installpolicy.Policy, error) { return maintenanceCallerPolicy(options.OwnerUserSID) },
		checkNoActiveSession:   func() error { return checkPersistedProtectionState(options.DataDirectory) },
		restoreSystemChanges:   RestoreRecordedSystemChanges,
		unregisterStartup:      UnregisterAgentStartup,
		deleteService:          DeleteWindowsService,
		scheduleProgramRemoval: ScheduleInstalledProgramRemoval,
		removeDataDirectory:    removeServiceDataDirectory,
		writeLog:               WriteUninstallResultLog,
		now:                    time.Now,
	}
	return uninstallWindows(options, dependencies)
}

func uninstallWindows(options UninstallOptions, dependencies uninstallerDependencies) (result UninstallResult, resultErr error) {
	expectedDataDirectory, err := dependencies.expectedDataDirectory()
	if err != nil {
		return UninstallResult{}, err
	}
	validated, err := dependencies.validate(options, expectedDataDirectory)
	if err != nil {
		return UninstallResult{}, err
	}
	installedPolicy, err := dependencies.loadPolicy(validated.DataDirectory)
	if err != nil {
		return UninstallResult{}, fmt.Errorf("load installed owner: %w", err)
	}
	installedManifest, err := dependencies.loadManifest(validated.DataDirectory)
	if err != nil {
		return UninstallResult{}, fmt.Errorf("load installed manifest: %w", err)
	}
	currentPolicy, err := dependencies.currentPolicy()
	if err != nil {
		return UninstallResult{}, fmt.Errorf("read current owner: %w", err)
	}
	if !strings.EqualFold(installedPolicy.OwnerUserSID, currentPolicy.OwnerUserSID) {
		return UninstallResult{}, installpolicy.ErrPolicyOwnerMismatch
	}
	stage := "confirm_data_disposition"
	dataRemoved, serviceRemoved := false, false
	defer func() {
		if resultErr == nil {
			return
		}
		result.CompletedUTC = dependencies.now().UTC()
		result.Disposition = validated.Disposition
		result.DataPreserved = !dataRemoved
		result.RestartRequired = serviceRemoved
		result.Stage, result.Failure = stage, resultErr.Error()
		if stage == "write_result_log" {
			return
		}
		logPath, logErr := dependencies.writeLog(result, installedPolicy.OwnerUserSID)
		result.LogPath = logPath
		if logErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("write uninstall failure log: %w", logErr))
		}
	}()
	if validated.Disposition != DataDispositionPreserve {
		wantConfirmation := "DELETE " + installedPolicy.OwnerUserSID
		if validated.DeletionConfirmation != wantConfirmation {
			return UninstallResult{}, ErrDataDeletionUnconfirmed
		}
	}
	stage = "check_session"
	if dependencies.beginMaintenance != nil {
		release, err := dependencies.beginMaintenance(validated.DataDirectory)
		if err != nil {
			return UninstallResult{}, err
		}
		defer release()
	}
	if err := dependencies.checkNoActiveSession(); err != nil {
		return UninstallResult{}, err
	}
	stage = "restore_system_changes"
	if err := dependencies.restoreSystemChanges(validated.DataDirectory); err != nil {
		return UninstallResult{}, fmt.Errorf("restore system changes: %w", err)
	}
	stage = "remove_startup"
	if err := dependencies.unregisterStartup(); err != nil {
		return UninstallResult{}, fmt.Errorf("remove agent startup: %w", err)
	}
	stage = "delete_service"
	if err := dependencies.deleteService(validated.ServiceName); err != nil {
		return UninstallResult{}, err
	}
	serviceRemoved = true
	if !validated.ManagedByMSI {
		stage = "schedule_program_removal"
		if err := dependencies.scheduleProgramRemoval(installedManifest); err != nil {
			return UninstallResult{}, err
		}
	}

	result = UninstallResult{
		CompletedUTC: dependencies.now().UTC(), Disposition: validated.Disposition,
		DataPreserved:   validated.Disposition == DataDispositionPreserve,
		RestartRequired: !validated.ManagedByMSI,
	}
	if validated.Disposition == DataDispositionExportAndDelete {
		result.ArchivePath = validated.ExportArchivePath
	}
	if !result.DataPreserved {
		stage = "remove_data"
		if err := dependencies.removeDataDirectory(validated.DataDirectory, expectedDataDirectory); err != nil {
			return UninstallResult{}, fmt.Errorf("delete service data: %w", err)
		}
		dataRemoved = true
	}
	stage = "write_result_log"
	result.Succeeded, result.Stage = true, "completed"
	logPath, err := dependencies.writeLog(result, installedPolicy.OwnerUserSID)
	if err != nil {
		result.Succeeded = false
		return result, fmt.Errorf("write uninstall result log: %w", err)
	}
	result.LogPath = logPath
	return result, nil
}

func expectedServiceDataDirectory() (string, error) {
	programData, err := winapi.KnownFolderPath(winapi.FOLDERID_ProgramData, winapi.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("resolve ProgramData directory: %w", err)
	}
	return filepath.Join(programData, "DesktopGuardPro"), nil
}

func checkNoActiveProtectionSession() error {
	result, err := appbridge.New().GetCurrentSession()
	if err != nil {
		var remoteError *appbridge.RemoteError
		if errors.As(err, &remoteError) && remoteError.Code == coreservice.ErrorCodeNoCurrentSession {
			return nil
		}
		return fmt.Errorf("query current protection session: %w", err)
	}
	if result.Session == nil {
		return errors.New("service returned an empty current session")
	}
	if result.Session.State != domain.SessionStateCompleted && result.Session.State != domain.SessionStateFailed {
		return fmt.Errorf("%w: state=%s", ErrActiveProtectionSession, result.Session.State)
	}
	return nil
}

func removeServiceDataDirectory(path, expected string) error {
	if !samePath(path, expected) {
		return ErrUninstallOptionsInvalid
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUninstallOptionsInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !samePath(resolved, path) {
		return ErrUninstallOptionsInvalid
	}
	return os.RemoveAll(path)
}
