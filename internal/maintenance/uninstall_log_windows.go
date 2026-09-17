package maintenance

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	platformwindows "desktopguardpro/internal/windows"

	winapi "golang.org/x/sys/windows"
)

const (
	uninstallLogDirectoryName = "DesktopGuardPro-UninstallLogs"
	uninstallLogSchemaVersion = 1
)

var ErrUninstallLogInvalid = errors.New("uninstall result log is invalid")

type uninstallLogDocument struct {
	SchemaVersion int             `json:"schemaVersion"`
	Result        UninstallResult `json:"result"`
}

func WriteUninstallResultLog(result UninstallResult, ownerUserSID string) (string, error) {
	programData, err := winapi.KnownFolderPath(winapi.FOLDERID_ProgramData, winapi.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("resolve uninstall log directory: %w", err)
	}
	directory := filepath.Join(programData, uninstallLogDirectoryName)
	if err := platformwindows.SecureOwnerDirectory(directory, ownerUserSID); err != nil {
		return "", fmt.Errorf("secure uninstall log directory: %w", err)
	}
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("create uninstall log id: %w", err)
	}
	return writeUninstallResultLog(directory, result, hex.EncodeToString(randomBytes))
}

func writeUninstallResultLog(directory string, result UninstallResult, suffix string) (string, error) {
	if result.CompletedUTC.IsZero() || result.CompletedUTC.Location() != time.UTC || suffix == "" {
		return "", ErrUninstallLogInvalid
	}
	document := uninstallLogDocument{SchemaVersion: uninstallLogSchemaVersion, Result: result}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode uninstall result log: %w", err)
	}
	encoded = append(encoded, '\n')
	name := fmt.Sprintf("uninstall-%s-%s.json", result.CompletedUTC.Format("20060102T150405Z"), suffix)
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create uninstall result log: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write uninstall result log: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("flush uninstall result log: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close uninstall result log: %w", err)
	}
	return path, nil
}
