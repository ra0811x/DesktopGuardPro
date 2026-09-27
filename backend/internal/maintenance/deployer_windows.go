package maintenance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	winapi "golang.org/x/sys/windows"
)

type DeployOptions struct {
	SourceDirectory  string
	InstallDirectory string
	DataDirectory    string
	ServiceName      string
	Version          string
}

type DeployResult struct {
	Install InstallResult `json:"install"`
	Copied  []string      `json:"copied"`
}

type deployerDependencies struct {
	isElevated      func() bool
	trustTarget     func(string) error
	verifySignature func(string) (SignatureIdentity, error)
	copyFile        func(string, string) error
	install         func(InstallOptions) (InstallResult, error)
}

func DeployWindows(options DeployOptions) (DeployResult, error) {
	if err := validateRuntimeConfiguration(options.DataDirectory, options.ServiceName); err != nil {
		return DeployResult{}, err
	}
	dependencies := deployerDependencies{
		isElevated:      func() bool { return winapi.GetCurrentProcessToken().IsElevated() },
		trustTarget:     (windowsPreflightProbe{}).TrustInstallDirectory,
		verifySignature: VerifyAuthenticodeComponent,
		copyFile:        copyReplacementFile,
		install:         InstallWindows,
	}
	return deployWindows(options, dependencies)
}

func deployWindows(options DeployOptions, dependencies deployerDependencies) (DeployResult, error) {
	if !dependencies.isElevated() {
		return DeployResult{}, ErrAdministratorRequired
	}
	sourceDirectory, err := validateExistingDirectory(options.SourceDirectory)
	if err != nil {
		return DeployResult{}, fmt.Errorf("source directory: %w", err)
	}
	installDirectory, err := normalizeAbsolutePath(options.InstallDirectory)
	if err != nil || samePath(sourceDirectory, installDirectory) {
		return DeployResult{}, ErrInstallLayoutInvalid
	}
	if err := dependencies.trustTarget(installDirectory); err != nil {
		return DeployResult{}, err
	}
	if !validNumericVersion(strings.TrimSpace(options.Version)) {
		return DeployResult{}, ErrVersionInvalid
	}
	components := []string{
		"desktop-guard-service.exe", "desktop-guard-ui.exe",
		"desktop-guard-agent.exe", "desktop-guard-maintenance.exe",
	}
	var signer SignatureIdentity
	for _, name := range components {
		path := filepath.Join(sourceDirectory, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 {
			return DeployResult{}, ErrComponentInvalid
		}
		identity, err := dependencies.verifySignature(path)
		if err != nil {
			return DeployResult{}, fmt.Errorf("verify source component %q: %w", name, err)
		}
		if signer.SHA256 == "" {
			signer = identity
		} else if !strings.EqualFold(signer.SHA256, identity.SHA256) {
			return DeployResult{}, ErrUpgradePublisherMismatch
		}
	}
	if _, err := os.Lstat(installDirectory); err == nil || !os.IsNotExist(err) {
		return DeployResult{}, ErrInstallLayoutInvalid
	}
	if err := os.Mkdir(installDirectory, 0o700); err != nil {
		return DeployResult{}, fmt.Errorf("create install directory: %w", err)
	}
	keepDirectory := true
	defer func() {
		if keepDirectory {
			_ = os.RemoveAll(installDirectory)
		}
	}()
	copied := make([]string, 0, len(components))
	for _, name := range components {
		destination := filepath.Join(installDirectory, name)
		if err := dependencies.copyFile(filepath.Join(sourceDirectory, name), destination); err != nil {
			return DeployResult{}, fmt.Errorf("copy component %q: %w", name, err)
		}
		copied = append(copied, destination)
	}
	installResult, err := dependencies.install(componentInstallOptions(
		installDirectory, options.DataDirectory, options.ServiceName, strings.TrimSpace(options.Version),
	))
	if err != nil {
		return DeployResult{}, err
	}
	keepDirectory = false
	return DeployResult{Install: installResult, Copied: copied}, nil
}
