package maintenance

import (
	"errors"
	"fmt"
	"time"

	"desktopguardpro/internal/installpolicy"
	platformwindows "desktopguardpro/internal/windows"
)

type InstallResult struct {
	Options        ValidatedInstallOptions
	Preflight      PreflightReport
	ServiceCreated bool
	OwnerUserSID   string
	Manifest       InstallManifest
}

type installerDependencies struct {
	validate                 func(InstallOptions) (ValidatedInstallOptions, error)
	preflight                func(ValidatedInstallOptions) (PreflightReport, error)
	buildManifest            func(ValidatedInstallOptions, PreflightReport, time.Time) (InstallManifest, error)
	configureService         func(ValidatedInstallOptions) (bool, error)
	secureData               func(path, serviceName string) error
	currentPolicy            func() (installpolicy.Policy, error)
	ensurePolicy             func(dataDirectory string, policy installpolicy.Policy) error
	migrateLegacySystemOwner func(dataDirectory string, policy installpolicy.Policy) error
	saveManifest             func(dataDirectory string, manifest InstallManifest) error
	registerStartup          func(agentExecutable string) error
	startService             func(serviceName string) error
	waitForHealth            func() error
	deleteService            func(serviceName string) error
	unregisterStartup        func() error
	now                      func() time.Time
}

func InstallWindows(options InstallOptions) (InstallResult, error) {
	if err := validateRuntimeConfiguration(options.DataDirectory, options.ServiceName); err != nil {
		return InstallResult{}, err
	}
	dependencies := installerDependencies{
		validate:                 ValidateInstallOptions,
		preflight:                RunWindowsInstallPreflight,
		buildManifest:            BuildInstallManifest,
		configureService:         ConfigureWindowsService,
		secureData:               platformwindows.SecureServiceDataDirectory,
		currentPolicy:            installpolicy.NewForCurrentUser,
		ensurePolicy:             installpolicy.EnsureOwner,
		migrateLegacySystemOwner: migrateLegacySystemOwner,
		saveManifest:             SaveInstallManifest,
		registerStartup:          RegisterAgentStartup,
		startService:             StartWindowsService,
		waitForHealth:            WaitForWindowsServiceHealth,
		deleteService:            DeleteWindowsService,
		unregisterStartup:        UnregisterAgentStartup,
		now:                      time.Now,
	}
	return installWindows(options, dependencies)
}

func installWindows(options InstallOptions, dependencies installerDependencies) (InstallResult, error) {
	validated, err := dependencies.validate(options)
	if err != nil {
		return InstallResult{}, fmt.Errorf("validate install options: %w", err)
	}
	preflight, err := dependencies.preflight(validated)
	if err != nil {
		return InstallResult{}, fmt.Errorf("run install preflight: %w", err)
	}
	manifest, err := dependencies.buildManifest(validated, preflight, dependencies.now().UTC())
	if err != nil {
		return InstallResult{}, fmt.Errorf("build install manifest: %w", err)
	}
	created, err := dependencies.configureService(validated)
	if err != nil {
		return InstallResult{}, err
	}

	rollbackFreshInstall := func(cause error, startupRegistered bool) error {
		if !created {
			return cause
		}
		var rollbackErrors []error
		if startupRegistered {
			rollbackErrors = append(rollbackErrors, dependencies.unregisterStartup())
		}
		rollbackErrors = append(rollbackErrors, dependencies.deleteService(validated.ServiceName))
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}
	if err := dependencies.secureData(validated.DataDirectory, validated.ServiceName); err != nil {
		return InstallResult{}, rollbackFreshInstall(fmt.Errorf("secure service data directory: %w", err), false)
	}
	policy, err := dependencies.currentPolicy()
	if validated.OwnerUserSID != "" {
		policy, err = installpolicy.New(validated.OwnerUserSID)
	}
	if err != nil {
		return InstallResult{}, rollbackFreshInstall(fmt.Errorf("resolve install owner: %w", err), false)
	}
	if err := dependencies.ensurePolicy(validated.DataDirectory, policy); err != nil {
		if validated.AllowLegacySystemOwnerMigration && errors.Is(err, installpolicy.ErrPolicyOwnerMismatch) {
			if migrationErr := dependencies.migrateLegacySystemOwner(validated.DataDirectory, policy); migrationErr != nil {
				return InstallResult{}, rollbackFreshInstall(fmt.Errorf("migrate legacy install policy: %w", migrationErr), false)
			}
			if retryErr := dependencies.ensurePolicy(validated.DataDirectory, policy); retryErr == nil {
				err = nil
			} else {
				err = retryErr
			}
		}
		if err != nil {
			return InstallResult{}, rollbackFreshInstall(fmt.Errorf("persist install policy: %w", err), false)
		}
	}
	if err := dependencies.saveManifest(validated.DataDirectory, manifest); err != nil {
		return InstallResult{}, rollbackFreshInstall(fmt.Errorf("persist install manifest: %w", err), false)
	}
	if err := dependencies.registerStartup(validated.AgentExecutable); err != nil {
		return InstallResult{}, rollbackFreshInstall(fmt.Errorf("register agent startup: %w", err), false)
	}
	if err := dependencies.startService(validated.ServiceName); err != nil {
		return InstallResult{}, rollbackFreshInstall(err, true)
	}
	if !validated.SkipServiceHealthCheck {
		if err := dependencies.waitForHealth(); err != nil {
			return InstallResult{}, rollbackFreshInstall(fmt.Errorf("verify service health: %w", err), true)
		}
	}
	return InstallResult{
		Options: validated, Preflight: preflight, ServiceCreated: created, OwnerUserSID: policy.OwnerUserSID, Manifest: manifest,
	}, nil
}

const legacySystemOwnerSID = "S-1-5-18"

func migrateLegacySystemOwner(dataDirectory string, desired installpolicy.Policy) error {
	existing, err := installpolicy.Load(dataDirectory)
	if err != nil {
		return fmt.Errorf("load existing install policy: %w", err)
	}
	if existing.OwnerUserSID != legacySystemOwnerSID {
		return installpolicy.ErrPolicyOwnerMismatch
	}
	if err := installpolicy.Save(dataDirectory, desired); err != nil {
		return fmt.Errorf("save migrated install policy: %w", err)
	}
	return nil
}
