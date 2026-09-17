package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type DataDisposition string

const (
	DataDispositionPreserve        DataDisposition = "preserve"
	DataDispositionExportAndDelete DataDisposition = "export_and_delete"
	DataDispositionDeleteNow       DataDisposition = "delete_now"
)

var (
	ErrUninstallOptionsInvalid = errors.New("uninstall options are invalid")
	ErrDataDeletionUnconfirmed = errors.New("data deletion is not confirmed")
)

type UninstallOptions struct {
	OwnerUserSID         string
	DataDirectory        string
	ServiceName          string
	ManagedByMSI         bool
	Disposition          DataDisposition
	ExportArchivePath    string
	DeletionConfirmation string
}

type ValidatedUninstallOptions struct {
	DataDirectory        string
	ServiceName          string
	ManagedByMSI         bool
	Disposition          DataDisposition
	ExportArchivePath    string
	DeletionConfirmation string
}

func ValidateUninstallOptions(options UninstallOptions, expectedDataDirectory string) (ValidatedUninstallOptions, error) {
	dataDirectory, err := normalizeAbsolutePath(options.DataDirectory)
	if err != nil {
		return ValidatedUninstallOptions{}, fmt.Errorf("data directory: %w", err)
	}
	expected, err := normalizeAbsolutePath(expectedDataDirectory)
	if err != nil || !samePath(dataDirectory, expected) {
		return ValidatedUninstallOptions{}, ErrUninstallOptionsInvalid
	}
	info, err := os.Lstat(dataDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ValidatedUninstallOptions{}, ErrUninstallOptionsInvalid
	}
	resolved, err := filepath.EvalSymlinks(dataDirectory)
	if err != nil || !samePath(resolved, dataDirectory) {
		return ValidatedUninstallOptions{}, ErrUninstallOptionsInvalid
	}
	serviceName := strings.TrimSpace(options.ServiceName)
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	if len(serviceName) > 256 || strings.ContainsAny(serviceName, `/\\\x00`) {
		return ValidatedUninstallOptions{}, ErrUninstallOptionsInvalid
	}
	validated := ValidatedUninstallOptions{
		DataDirectory: dataDirectory, ServiceName: serviceName, ManagedByMSI: options.ManagedByMSI, Disposition: options.Disposition,
		DeletionConfirmation: strings.TrimSpace(options.DeletionConfirmation),
	}
	switch options.Disposition {
	case DataDispositionPreserve:
		return validated, nil
	case DataDispositionExportAndDelete:
		if validated.DeletionConfirmation == "" {
			return ValidatedUninstallOptions{}, ErrDataDeletionUnconfirmed
		}
		archive, err := validateExportArchive(options.ExportArchivePath, dataDirectory)
		if err != nil {
			return ValidatedUninstallOptions{}, err
		}
		validated.ExportArchivePath = archive
		return validated, nil
	case DataDispositionDeleteNow:
		if validated.DeletionConfirmation == "" {
			return ValidatedUninstallOptions{}, ErrDataDeletionUnconfirmed
		}
		return validated, nil
	default:
		return ValidatedUninstallOptions{}, ErrUninstallOptionsInvalid
	}
}

func validateExportArchive(path, dataDirectory string) (string, error) {
	normalized, err := normalizeAbsolutePath(path)
	if err != nil || pathWithin(dataDirectory, normalized) || samePath(dataDirectory, normalized) {
		return "", ErrUninstallOptionsInvalid
	}
	info, err := os.Lstat(normalized)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 {
		return "", ErrUninstallOptionsInvalid
	}
	resolved, err := filepath.EvalSymlinks(normalized)
	if err != nil || !samePath(resolved, normalized) {
		return "", ErrUninstallOptionsInvalid
	}
	return normalized, nil
}
