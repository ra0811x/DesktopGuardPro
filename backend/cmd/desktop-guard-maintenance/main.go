package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"desktopguardpro/internal/installpolicy"
	"desktopguardpro/internal/maintenance"
)

const (
	exitSuccess       = 0
	exitOperation     = 1
	exitUsage         = 2
	exitAuthorization = 3
	exitActiveSession = 4
	exitVerification  = 5
)

type commandOperations struct {
	msi       func(string, maintenance.MSIOptions) (maintenance.MSIResult, error)
	deploy    func(maintenance.DeployOptions) (maintenance.DeployResult, error)
	install   func(maintenance.InstallOptions) (maintenance.InstallResult, error)
	upgrade   func(maintenance.UpgradeOptions) (maintenance.UpgradeResult, error)
	uninstall func(maintenance.UninstallOptions) (maintenance.UninstallResult, error)
	verify    func(string) (maintenance.VerificationResult, error)
}

func main() {
	operations := commandOperations{
		msi:     maintenance.MSIWindows,
		deploy:  maintenance.DeployWindows,
		install: maintenance.InstallWindows, upgrade: maintenance.UpgradeWindows,
		uninstall: maintenance.UninstallWindows, verify: maintenance.VerifyWindowsInstallation,
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, operations))
}

func run(args []string, stdout, stderr io.Writer, operations commandOperations) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: desktop-guard-maintenance <deploy|install|upgrade|uninstall|verify|diagnose|msi> [options]")
		return exitUsage
	}
	var result any
	var err error
	switch args[0] {
	case "msi":
		result, err = runMSI(args[1:], stderr, operations.msi)
	case "deploy":
		result, err = runDeploy(args[1:], stderr, operations.deploy)
	case "install":
		result, err = runInstall(args[1:], stderr, operations.install)
	case "upgrade":
		result, err = runUpgrade(args[1:], stderr, operations.upgrade)
	case "uninstall":
		result, err = runUninstall(args[1:], stderr, operations.uninstall)
	case "verify", "diagnose":
		result, err = runVerify(args[1:], stderr, operations.verify)
		if args[0] == "diagnose" && result != nil {
			err = nil
		}
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return exitUsage
	}
	if result != nil {
		// Release builds use the Windows GUI subsystem so MSI custom actions do
		// not open a console window. A missing stdout handle must not turn a
		// completed maintenance operation into an installer rollback.
		_ = json.NewEncoder(stdout).Encode(result)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitCodeFor(err)
	}
	return exitSuccess
}

func runMSI(args []string, stderr io.Writer, operation func(string, maintenance.MSIOptions) (maintenance.MSIResult, error)) (any, error) {
	flags := flag.NewFlagSet("msi", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stage := flags.String("stage", "", "prepare, apply, stop-rollback, rollback, or commit")
	installDirectory := flags.String("install-dir", "", "MSI installation directory")
	dataDirectory := flags.String("data-dir", "", "protected service data directory")
	ownerSID := flags.String("owner-sid", "", "owner SID forwarded only by elevated LocalSystem")
	transactionID := flags.String("transaction-id", "", "MSI product code for this transaction")
	version := flags.String("version", "", "target product version")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %v", maintenance.ErrUpgradeOptionsInvalid, err)
	}
	return operation(*stage, maintenance.MSIOptions{InstallDirectory: *installDirectory, DataDirectory: *dataDirectory, OwnerUserSID: *ownerSID, TransactionID: *transactionID, Version: *version})
}

func runDeploy(args []string, stderr io.Writer, deploy func(maintenance.DeployOptions) (maintenance.DeployResult, error)) (any, error) {
	flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourceDirectory := flags.String("source-dir", "", "extracted signed release directory")
	installDirectory := flags.String("install-dir", "", "new Program Files installation directory")
	dataDirectory := flags.String("data-dir", "", "service data directory")
	version := flags.String("version", "", "numeric product version")
	serviceName := flags.String("service-name", maintenance.DefaultServiceName, "Windows service name")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %v", maintenance.ErrInstallPathInvalid, err)
	}
	return deploy(maintenance.DeployOptions{
		SourceDirectory: *sourceDirectory, InstallDirectory: *installDirectory,
		DataDirectory: *dataDirectory, Version: *version, ServiceName: *serviceName,
	})
}

func runInstall(args []string, stderr io.Writer, install func(maintenance.InstallOptions) (maintenance.InstallResult, error)) (any, error) {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	installDirectory := flags.String("install-dir", "", "directory containing signed executables")
	dataDirectory := flags.String("data-dir", "", "service data directory")
	ownerUserSID := flags.String("owner-sid", "", "Windows SID of the installation owner")
	allowLegacySystemOwnerMigration := flags.Bool("allow-legacy-system-owner-migration", false, "migrate only the legacy LocalSystem-owned policy")
	skipServiceHealthCheck := flags.Bool("skip-service-health-check", false, "skip the service health probe for an elevated installer action")
	version := flags.String("version", "", "numeric product version")
	serviceName := flags.String("service-name", maintenance.DefaultServiceName, "Windows service name")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %v", maintenance.ErrInstallPathInvalid, err)
	}
	return install(maintenance.InstallOptions{
		InstallDirectory: *installDirectory, DataDirectory: *dataDirectory, OwnerUserSID: *ownerUserSID,
		AllowLegacySystemOwnerMigration: *allowLegacySystemOwnerMigration,
		SkipServiceHealthCheck:          *skipServiceHealthCheck,
		Version:                         *version, ServiceName: *serviceName,
		ServiceExecutable: filepath.Join(*installDirectory, "desktop-guard-service.exe"),
		UIExecutable:      filepath.Join(*installDirectory, "desktop-guard-ui.exe"),
		AgentExecutable:   filepath.Join(*installDirectory, "desktop-guard-agent.exe"),
	})
}

func runUpgrade(args []string, stderr io.Writer, upgrade func(maintenance.UpgradeOptions) (maintenance.UpgradeResult, error)) (any, error) {
	flags := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	installDirectory := flags.String("install-dir", "", "current installation directory")
	stagedDirectory := flags.String("staged-dir", "", "signed staged component directory")
	dataDirectory := flags.String("data-dir", "", "service data directory")
	currentVersion := flags.String("current-version", "", "installed product version")
	targetVersion := flags.String("target-version", "", "target product version")
	serviceName := flags.String("service-name", maintenance.DefaultServiceName, "Windows service name")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %v", maintenance.ErrUpgradeOptionsInvalid, err)
	}
	return upgrade(maintenance.UpgradeOptions{
		InstallDirectory: *installDirectory, StagedDirectory: *stagedDirectory, DataDirectory: *dataDirectory,
		CurrentVersion: *currentVersion, TargetVersion: *targetVersion, ServiceName: *serviceName,
	})
}

func runUninstall(args []string, stderr io.Writer, uninstall func(maintenance.UninstallOptions) (maintenance.UninstallResult, error)) (any, error) {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data-dir", "", "service data directory")
	ownerUserSID := flags.String("owner-sid", "", "installation owner forwarded only by an elevated LocalSystem installer action")
	managedByMSI := flags.Bool("msi-managed", false, "leave program-file removal to Windows Installer")
	mode := flags.String("data", string(maintenance.DataDispositionPreserve), "preserve, export_and_delete, or delete_now")
	archive := flags.String("export-archive", "", "existing exported audit archive")
	confirmation := flags.String("confirm-delete", "", "exact DELETE plus owner SID confirmation")
	serviceName := flags.String("service-name", maintenance.DefaultServiceName, "Windows service name")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %v", maintenance.ErrUninstallOptionsInvalid, err)
	}
	return uninstall(maintenance.UninstallOptions{
		OwnerUserSID: *ownerUserSID, ManagedByMSI: *managedByMSI,
		DataDirectory: *dataDirectory, ServiceName: *serviceName, Disposition: maintenance.DataDisposition(*mode),
		ExportArchivePath: *archive, DeletionConfirmation: *confirmation,
	})
}

func runVerify(args []string, stderr io.Writer, verify func(string) (maintenance.VerificationResult, error)) (any, error) {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data-dir", "", "service data directory")
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %v", maintenance.ErrUninstallOptionsInvalid, err)
	}
	result, err := verify(*dataDirectory)
	return result, err
}

func exitCodeFor(err error) int {
	switch {
	case errors.Is(err, installpolicy.ErrPolicyOwnerMismatch), errors.Is(err, maintenance.ErrAdministratorRequired):
		return exitAuthorization
	case errors.Is(err, maintenance.ErrActiveProtectionSession):
		return exitActiveSession
	case errors.Is(err, maintenance.ErrInstallPathInvalid), errors.Is(err, maintenance.ErrUpgradeOptionsInvalid), errors.Is(err, maintenance.ErrUninstallOptionsInvalid), errors.Is(err, maintenance.ErrUnsupportedRuntimeConfiguration):
		return exitUsage
	case err != nil && err.Error() == "installation verification failed":
		return exitVerification
	default:
		return exitOperation
	}
}
