package maintenance

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrUpgradeOptionsInvalid = errors.New("upgrade options are invalid")

type UpgradeOptions struct {
	InstallDirectory string
	StagedDirectory  string
	DataDirectory    string
	ServiceName      string
	CurrentVersion   string
	TargetVersion    string
}

type ValidatedUpgradeOptions struct {
	Current ValidatedInstallOptions
	Staged  ValidatedInstallOptions
}

func ValidateUpgradeOptions(options UpgradeOptions) (ValidatedUpgradeOptions, error) {
	if samePath(options.InstallDirectory, options.StagedDirectory) || !versionGreater(options.TargetVersion, options.CurrentVersion) {
		return ValidatedUpgradeOptions{}, ErrUpgradeOptionsInvalid
	}
	current, err := ValidateInstallOptions(componentInstallOptions(
		options.InstallDirectory, options.DataDirectory, options.ServiceName, options.CurrentVersion,
	))
	if err != nil {
		return ValidatedUpgradeOptions{}, fmt.Errorf("current installation: %w", err)
	}
	staged, err := ValidateInstallOptions(componentInstallOptions(
		options.StagedDirectory, options.DataDirectory, options.ServiceName, options.TargetVersion,
	))
	if err != nil {
		return ValidatedUpgradeOptions{}, fmt.Errorf("staged installation: %w", err)
	}
	if pathsOverlap(current.InstallDirectory, staged.InstallDirectory) {
		return ValidatedUpgradeOptions{}, ErrUpgradeOptionsInvalid
	}
	return ValidatedUpgradeOptions{Current: current, Staged: staged}, nil
}

func componentInstallOptions(directory, dataDirectory, serviceName, version string) InstallOptions {
	return InstallOptions{
		InstallDirectory: directory, DataDirectory: dataDirectory, ServiceName: serviceName, Version: version,
		ServiceExecutable: filepath.Join(directory, "desktop-guard-service.exe"),
		UIExecutable:      filepath.Join(directory, "desktop-guard-ui.exe"),
		AgentExecutable:   filepath.Join(directory, "desktop-guard-agent.exe"),
	}
}

func versionGreater(candidate, current string) bool {
	if !validNumericVersion(candidate) || !validNumericVersion(current) {
		return false
	}
	candidateParts := strings.Split(candidate, ".")
	currentParts := strings.Split(current, ".")
	for len(candidateParts) < 4 {
		candidateParts = append(candidateParts, "0")
	}
	for len(currentParts) < 4 {
		currentParts = append(currentParts, "0")
	}
	for index := 0; index < 4; index++ {
		candidateValue, _ := strconv.ParseUint(candidateParts[index], 10, 16)
		currentValue, _ := strconv.ParseUint(currentParts[index], 10, 16)
		if candidateValue != currentValue {
			return candidateValue > currentValue
		}
	}
	return false
}
