package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"desktopguardpro/internal/installpolicy"
	"desktopguardpro/internal/maintenancegate"
	"golang.org/x/sys/windows/svc/mgr"
)

type MSIOptions struct {
	InstallDirectory string `json:"installDirectory"`
	DataDirectory    string `json:"dataDirectory"`
	OwnerUserSID     string `json:"ownerUserSid,omitempty"`
	TransactionID    string `json:"transactionId"`
	Version          string `json:"version"`
}

type MSIResult struct {
	Stage         string `json:"stage"`
	TransactionID string `json:"transactionId"`
}

type msiStartupSnapshot struct {
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
	Expand  bool   `json:"expand,omitempty"`
}

type msiSnapshot struct {
	ServiceConfig  *mgr.Config        `json:"serviceConfig,omitempty"`
	OwnerSID       string             `json:"ownerSid,omitempty"`
	Manifest       InstallManifest    `json:"manifest"`
	ManifestJSON   []byte             `json:"manifestJson,omitempty"`
	PolicyJSON     []byte             `json:"policyJson,omitempty"`
	ServiceExisted bool               `json:"serviceExisted"`
	ServiceRunning bool               `json:"serviceRunning"`
	Startup        msiStartupSnapshot `json:"startup"`
}

type msiJournal struct {
	DataBackupReady     bool              `json:"dataBackupReady"`
	BackupDirectoryName string            `json:"backupDirectoryName,omitempty"`
	DataFiles           []msiDataCopy     `json:"dataFiles,omitempty"`
	SchemaVersion       int               `json:"schemaVersion"`
	Stage               string            `json:"stage"`
	Options             MSIOptions        `json:"options"`
	OwnerSID            string            `json:"ownerSid"`
	Signer              SignatureIdentity `json:"signer"`
	Before              msiSnapshot       `json:"before"`
}

type msiDataCopy struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type msiDependencies struct {
	backup       func(*msiJournal) error
	ownerSID     string
	signer       SignatureIdentity
	snapshot     func() (msiSnapshot, error)
	checkSession func(msiSnapshot) error
	stop         func() error
	apply        func(msiJournal) error
	restore      func(msiJournal) error
	finish       func(msiJournal) error
}

// Windows Installer restores the application files and native runtime. This journal keeps
// non-MSI state and the session freeze alive through all deferred/rollback
// processes, including a failure after the new service has passed its probe.
func runMSITransaction(ctx context.Context, stage string, options MSIOptions, dependencies msiDependencies) (MSIResult, error) {
	gate, err := maintenancegate.Acquire(ctx, options.DataDirectory)
	if err != nil {
		return MSIResult{}, err
	}
	defer gate.Close()
	path := filepath.Join(options.DataDirectory, maintenancegate.JournalFileName)
	result := MSIResult{Stage: stage, TransactionID: options.TransactionID}
	if stage == "prepare" {
		if err := gate.CheckReady(); err != nil {
			return result, err
		}
		before, err := dependencies.snapshot()
		if err != nil {
			return result, err
		}
		if before.OwnerSID != "" && !strings.EqualFold(before.OwnerSID, dependencies.ownerSID) {
			return result, installpolicy.ErrPolicyOwnerMismatch
		}
		if before.Manifest.SignerSHA256 != "" &&
			!strings.EqualFold(before.Manifest.SignerSHA256, dependencies.signer.SHA256) &&
			!isRaymondMSIPublisherMigration(before.Manifest.SignerSHA256, dependencies.signer.SHA256) {
			return result, ErrUpgradePublisherMismatch
		}
		if err := dependencies.checkSession(before); err != nil {
			return result, err
		}
		journal := msiJournal{SchemaVersion: 1, Stage: "preparing", Options: options, OwnerSID: dependencies.ownerSID, Signer: dependencies.signer, Before: before}
		if err := saveMSIJournal(path, journal); err != nil {
			return result, err
		}
		if err := dependencies.stop(); err != nil {
			return result, err
		}
		if err := dependencies.backup(&journal); err != nil {
			return result, err
		}
		journal.Stage, journal.DataBackupReady = "prepared", true
		return result, saveMSIJournal(path, journal)
	}
	journal, err := loadMSIJournal(path)
	if os.IsNotExist(err) && (stage == "rollback" || stage == "stop-rollback") {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if journal.Options.TransactionID != options.TransactionID || journal.Options.Version != options.Version ||
		!samePath(journal.Options.InstallDirectory, options.InstallDirectory) || !samePath(journal.Options.DataDirectory, options.DataDirectory) {
		return result, errors.New("MSI transaction does not match pending recovery state")
	}
	if !strings.EqualFold(journal.OwnerSID, dependencies.ownerSID) {
		return result, installpolicy.ErrPolicyOwnerMismatch
	}
	if !strings.EqualFold(journal.Signer.SHA256, dependencies.signer.SHA256) {
		return result, ErrUpgradePublisherMismatch
	}
	switch stage {
	case "apply":
		if journal.Stage != "prepared" || !journal.DataBackupReady {
			return result, errors.New("MSI transaction is not prepared")
		}
		if err := dependencies.apply(journal); err != nil {
			return result, err
		}
		journal.Stage = "applied"
		return result, saveMSIJournal(path, journal)
	case "stop-rollback":
		return result, dependencies.stop()
	case "rollback":
		if err := dependencies.stop(); err != nil {
			return result, err
		}
		if err := dependencies.restore(journal); err != nil {
			return result, fmt.Errorf("MSI rollback remains pending: %w", err)
		}
		return result, os.Remove(path)
	case "commit":
		if journal.Stage != "applied" {
			return result, errors.New("MSI transaction has not passed application and health verification")
		}
		if err := dependencies.finish(journal); err != nil {
			return result, err
		}
		return result, os.Remove(path)
	default:
		return result, errors.New("unknown MSI transaction stage")
	}
}

// MSIWindows verifies the caller's signature before entering the transaction.
// After the owner check, allow only the retired test certificate to move to the
// pinned Raymond certificate. The original manifest stays in the rollback state.
func isRaymondMSIPublisherMigration(previous, current string) bool {
	const retired = "464d3524d6fbc620538da0806fc260710a08d0ab77a854b111cb033cd91a4230"
	const raymond = "670117e1bd2c3dc9a61724a3176e88cb0d6b608e37360e094881770c0813ef71"
	return strings.EqualFold(previous, retired) && strings.EqualFold(current, raymond)
}

func saveMSIJournal(path string, journal msiJournal) error {
	encoded, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return writeMSIStateFile(path, encoded)
}

func writeMSIStateFile(path string, encoded []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".maintenance-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func loadMSIJournal(path string) (msiJournal, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return msiJournal{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4*maximumManifestSize {
		return msiJournal{}, errors.New("invalid MSI journal file")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return msiJournal{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var journal msiJournal
	if err := decoder.Decode(&journal); err != nil {
		return journal, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return journal, errors.New("invalid MSI journal trailer")
	}
	if journal.SchemaVersion != 1 || (journal.Stage != "preparing" && journal.Stage != "prepared" && journal.Stage != "applied") || journal.OwnerSID == "" || journal.Signer.SHA256 == "" {
		return journal, errors.New("invalid MSI journal state")
	}
	if journal.BackupDirectoryName != "" && !validMSIBackupDirectoryName(journal) {
		return journal, errors.New("invalid MSI backup directory")
	}
	return journal, nil
}
