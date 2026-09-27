package maintenance

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/installpolicy"
)

type VerificationCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type VerificationResult struct {
	CheckedUTC time.Time           `json:"checkedUtc"`
	Healthy    bool                `json:"healthy"`
	Checks     []VerificationCheck `json:"checks"`
}

type verificationDependencies struct {
	expectedDataDirectory func() (string, error)
	loadPolicy            func(string) (installpolicy.Policy, error)
	currentPolicy         func() (installpolicy.Policy, error)
	loadManifest          func(string) (InstallManifest, error)
	verifyComponents      func(InstallManifest) error
	preflight             func(ValidatedInstallOptions) (PreflightReport, error)
	loadRestoration       func(string) (RestorationJournal, error)
	waitForHealth         func() error
	now                   func() time.Time
}

func VerifyWindowsInstallation(dataDirectory string) (VerificationResult, error) {
	dependencies := verificationDependencies{
		expectedDataDirectory: expectedServiceDataDirectory,
		loadPolicy:            installpolicy.Load, currentPolicy: installpolicy.NewForCurrentUser,
		loadManifest: LoadInstallManifest, verifyComponents: VerifyInstalledComponents,
		preflight: RunWindowsInstallPreflight, loadRestoration: LoadRestorationJournal,
		waitForHealth: WaitForWindowsServiceHealth, now: time.Now,
	}
	return verifyWindowsInstallation(dataDirectory, dependencies)
}

func verifyWindowsInstallation(dataDirectory string, dependencies verificationDependencies) (VerificationResult, error) {
	result := VerificationResult{CheckedUTC: dependencies.now().UTC(), Healthy: true}
	addCheck := func(name string, err error) {
		check := VerificationCheck{Name: name, Passed: err == nil}
		if err != nil {
			check.Message = err.Error()
			result.Healthy = false
		}
		result.Checks = append(result.Checks, check)
	}
	expected, err := dependencies.expectedDataDirectory()
	if err != nil || !samePath(dataDirectory, expected) {
		if err == nil {
			err = ErrUninstallOptionsInvalid
		}
		addCheck("data_directory", err)
		return result, err
	}
	addCheck("data_directory", nil)
	installedPolicy, policyErr := dependencies.loadPolicy(dataDirectory)
	currentPolicy, currentErr := dependencies.currentPolicy()
	ownerErr := errors.Join(policyErr, currentErr)
	if ownerErr == nil && !strings.EqualFold(installedPolicy.OwnerUserSID, currentPolicy.OwnerUserSID) {
		ownerErr = installpolicy.ErrPolicyOwnerMismatch
	}
	addCheck("owner_policy", ownerErr)

	manifest, manifestErr := dependencies.loadManifest(dataDirectory)
	addCheck("install_manifest", manifestErr)
	if manifestErr == nil {
		addCheck("component_hashes", dependencies.verifyComponents(manifest))
		options, optionsErr := installOptionsFromManifest(manifest, dataDirectory)
		if optionsErr != nil {
			addCheck("component_signatures", optionsErr)
		} else {
			preflight, preflightErr := dependencies.preflight(options)
			if preflightErr == nil && !strings.EqualFold(preflight.SignerSHA256, manifest.SignerSHA256) {
				preflightErr = ErrUpgradePublisherMismatch
			}
			addCheck("component_signatures", preflightErr)
		}
	}
	_, restorationErr := dependencies.loadRestoration(dataDirectory)
	addCheck("restoration_journal", restorationErr)
	addCheck("service_health", dependencies.waitForHealth())
	if !result.Healthy {
		return result, errors.New("installation verification failed")
	}
	return result, nil
}

func installOptionsFromManifest(manifest InstallManifest, dataDirectory string) (ValidatedInstallOptions, error) {
	paths := make(map[string]string, len(manifest.Components))
	for _, component := range manifest.Components {
		paths[component.Name] = component.Path
	}
	servicePath, serviceOK := paths["service"]
	uiPath, uiOK := paths["ui"]
	agentPath, agentOK := paths["agent"]
	if !serviceOK || !uiOK || !agentOK {
		return ValidatedInstallOptions{}, ErrInstallManifestInvalid
	}
	return ValidatedInstallOptions{
		InstallDirectory: filepath.Dir(servicePath), DataDirectory: dataDirectory,
		ServiceExecutable: servicePath, UIExecutable: uiPath, AgentExecutable: agentPath,
		ServiceName: DefaultServiceName, Version: manifest.ProductVersion,
	}, nil
}
