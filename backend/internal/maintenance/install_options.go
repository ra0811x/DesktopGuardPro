package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"desktopguardpro/internal/installpolicy"
)

const DefaultServiceName = "DesktopGuardPro"

var (
	ErrInstallPathInvalid   = errors.New("install path is invalid")
	ErrInstallLayoutInvalid = errors.New("install layout is invalid")
	ErrComponentInvalid     = errors.New("install component is invalid")
	ErrServiceNameInvalid   = errors.New("service name is invalid")
	ErrVersionInvalid       = errors.New("install version is invalid")
)

type InstallOptions struct {
	InstallDirectory                string
	DataDirectory                   string
	ServiceExecutable               string
	UIExecutable                    string
	AgentExecutable                 string
	OwnerUserSID                    string
	AllowLegacySystemOwnerMigration bool
	SkipServiceHealthCheck          bool
	ServiceName                     string
	Version                         string
}

type ValidatedInstallOptions struct {
	InstallDirectory                string
	DataDirectory                   string
	ServiceExecutable               string
	UIExecutable                    string
	AgentExecutable                 string
	OwnerUserSID                    string
	AllowLegacySystemOwnerMigration bool
	SkipServiceHealthCheck          bool
	ServiceName                     string
	Version                         string
}

func ValidateInstallOptions(options InstallOptions) (ValidatedInstallOptions, error) {
	installDirectory, err := validateExistingDirectory(options.InstallDirectory)
	if err != nil {
		return ValidatedInstallOptions{}, fmt.Errorf("install directory: %w", err)
	}
	dataDirectory, err := normalizeAbsolutePath(options.DataDirectory)
	if err != nil {
		return ValidatedInstallOptions{}, fmt.Errorf("data directory: %w", err)
	}
	if pathsOverlap(installDirectory, dataDirectory) {
		return ValidatedInstallOptions{}, ErrInstallLayoutInvalid
	}

	serviceExecutable, err := validateComponent(installDirectory, options.ServiceExecutable)
	if err != nil {
		return ValidatedInstallOptions{}, fmt.Errorf("service executable: %w", err)
	}
	uiExecutable, err := validateComponent(installDirectory, options.UIExecutable)
	if err != nil {
		return ValidatedInstallOptions{}, fmt.Errorf("UI executable: %w", err)
	}
	agentExecutable, err := validateComponent(installDirectory, options.AgentExecutable)
	if err != nil {
		return ValidatedInstallOptions{}, fmt.Errorf("agent executable: %w", err)
	}
	maintenanceExecutable, err := validateComponent(installDirectory, filepath.Join(installDirectory, "desktop-guard-maintenance.exe"))
	if err != nil {
		return ValidatedInstallOptions{}, fmt.Errorf("maintenance executable: %w", err)
	}
	if samePath(maintenanceExecutable, serviceExecutable) || samePath(maintenanceExecutable, uiExecutable) || samePath(maintenanceExecutable, agentExecutable) {
		return ValidatedInstallOptions{}, ErrInstallLayoutInvalid
	}
	if samePath(serviceExecutable, uiExecutable) || samePath(serviceExecutable, agentExecutable) || samePath(uiExecutable, agentExecutable) {
		return ValidatedInstallOptions{}, ErrInstallLayoutInvalid
	}
	ownerUserSID := strings.TrimSpace(options.OwnerUserSID)
	if ownerUserSID != "" {
		policy, err := installpolicy.New(ownerUserSID)
		if err != nil {
			return ValidatedInstallOptions{}, fmt.Errorf("install owner: %w", err)
		}
		ownerUserSID = policy.OwnerUserSID
	}

	serviceName := strings.TrimSpace(options.ServiceName)
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	if len(serviceName) > 256 || strings.ContainsAny(serviceName, `/\\\x00`) {
		return ValidatedInstallOptions{}, ErrServiceNameInvalid
	}
	version := strings.TrimSpace(options.Version)
	if !validNumericVersion(version) {
		return ValidatedInstallOptions{}, ErrVersionInvalid
	}

	return ValidatedInstallOptions{
		InstallDirectory:                installDirectory,
		DataDirectory:                   dataDirectory,
		ServiceExecutable:               serviceExecutable,
		UIExecutable:                    uiExecutable,
		AgentExecutable:                 agentExecutable,
		OwnerUserSID:                    ownerUserSID,
		AllowLegacySystemOwnerMigration: options.AllowLegacySystemOwnerMigration,
		SkipServiceHealthCheck:          options.SkipServiceHealthCheck,
		ServiceName:                     serviceName,
		Version:                         version,
	}, nil
}

func (options ValidatedInstallOptions) maintenanceExecutable() string {
	directory := options.InstallDirectory
	if directory == "" {
		directory = filepath.Dir(options.ServiceExecutable)
	}
	return filepath.Join(directory, "desktop-guard-maintenance.exe")
}

func validateExistingDirectory(path string) (string, error) {
	normalized, err := normalizeAbsolutePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(normalized)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrInstallPathInvalid
	}
	resolved, err := filepath.EvalSymlinks(normalized)
	if err != nil || !samePath(normalized, resolved) {
		return "", ErrInstallPathInvalid
	}
	return normalized, nil
}

func validateComponent(installDirectory, path string) (string, error) {
	normalized, err := normalizeAbsolutePath(path)
	if err != nil || !pathWithin(installDirectory, normalized) || !strings.EqualFold(filepath.Ext(normalized), ".exe") {
		return "", ErrComponentInvalid
	}
	info, err := os.Lstat(normalized)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrComponentInvalid
	}
	resolved, err := filepath.EvalSymlinks(normalized)
	if err != nil || !samePath(normalized, resolved) {
		return "", ErrComponentInvalid
	}
	return normalized, nil
}

func normalizeAbsolutePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return "", ErrInstallPathInvalid
	}
	return filepath.Clean(path), nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, `..`+string(filepath.Separator))
}

func pathsOverlap(left, right string) bool {
	return samePath(left, right) || pathWithin(left, right) || pathWithin(right, left)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func validNumericVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		value, err := strconv.ParseUint(part, 10, 16)
		if err != nil || strconv.FormatUint(value, 10) != part {
			return false
		}
	}
	return true
}
