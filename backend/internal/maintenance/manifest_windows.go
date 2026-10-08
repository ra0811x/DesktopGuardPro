package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	InstallManifestFileName = "install-manifest.json"
	installManifestVersion  = 2
	maximumManifestSize     = 1024 * 1024
)

var ErrInstallManifestInvalid = errors.New("install manifest is invalid")

type InstallComponentRecord struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

type InstallManifest struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	ProductVersion  string                   `json:"productVersion"`
	InstalledUTC    time.Time                `json:"installedUtc"`
	WindowsBuild    uint32                   `json:"windowsBuild"`
	WebView2Version string                   `json:"webView2Version"`
	SignerSHA256    string                   `json:"signerSha256"`
	SignerSubject   string                   `json:"signerSubject"`
	Components      []InstallComponentRecord `json:"components"`
	RuntimeFiles    []RuntimeFileRecord      `json:"runtimeFiles,omitempty"`
}

func BuildInstallManifest(options ValidatedInstallOptions, preflight PreflightReport, installedUTC time.Time) (InstallManifest, error) {
	if !validNumericVersion(options.Version) || installedUTC.IsZero() || len(preflight.SignerSHA256) != sha256.Size*2 || preflight.SignerSubject == "" {
		return InstallManifest{}, ErrInstallManifestInvalid
	}
	if _, err := hex.DecodeString(preflight.SignerSHA256); err != nil {
		return InstallManifest{}, ErrInstallManifestInvalid
	}
	installedUTC = installedUTC.UTC()
	components := []struct {
		name string
		path string
	}{
		{name: "service", path: options.ServiceExecutable},
		{name: "ui", path: options.UIExecutable},
		{name: "agent", path: options.AgentExecutable},
		{name: "maintenance", path: options.maintenanceExecutable()},
	}
	records := make([]InstallComponentRecord, 0, len(components))
	for _, component := range components {
		record, err := inspectInstallComponent(component.name, component.path)
		if err != nil {
			return InstallManifest{}, err
		}
		records = append(records, record)
	}
	runtimeFiles, err := readReleaseRuntimeFiles(options.InstallDirectory, false)
	if err != nil {
		return InstallManifest{}, err
	}
	return InstallManifest{
		SchemaVersion: installManifestVersion, ProductVersion: options.Version, InstalledUTC: installedUTC,
		WindowsBuild: preflight.WindowsBuild, WebView2Version: preflight.WebView2Version,
		SignerSHA256: preflight.SignerSHA256, SignerSubject: preflight.SignerSubject, Components: records, RuntimeFiles: runtimeFiles,
	}, nil
}

func SaveInstallManifest(dataDirectory string, manifest InstallManifest) error {
	if err := validateInstallManifest(manifest); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode install manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maximumManifestSize {
		return ErrInstallManifestInvalid
	}
	temporary, err := os.CreateTemp(dataDirectory, ".install-manifest-*")
	if err != nil {
		return fmt.Errorf("create temporary install manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary install manifest: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write temporary install manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary install manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary install manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(dataDirectory, InstallManifestFileName)); err != nil {
		return fmt.Errorf("replace install manifest: %w", err)
	}
	keepTemporary = false
	return nil
}

func LoadInstallManifest(dataDirectory string) (InstallManifest, error) {
	path := filepath.Join(dataDirectory, InstallManifestFileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumManifestSize {
		return InstallManifest{}, ErrInstallManifestInvalid
	}
	encoded, err := os.ReadFile(path)
	if err != nil || len(encoded) > maximumManifestSize {
		return InstallManifest{}, ErrInstallManifestInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest InstallManifest
	if err := decoder.Decode(&manifest); err != nil {
		return InstallManifest{}, fmt.Errorf("%w: %v", ErrInstallManifestInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return InstallManifest{}, ErrInstallManifestInvalid
	}
	if err := validateInstallManifest(manifest); err != nil {
		return InstallManifest{}, err
	}
	return manifest, nil
}

func VerifyInstalledComponents(manifest InstallManifest) error {
	return verifyInstalledComponents(manifest, VerifyAuthenticodeComponent)
}

func verifyInstalledComponents(manifest InstallManifest, verifySignature func(string) (SignatureIdentity, error)) error {
	if err := validateInstallManifest(manifest); err != nil {
		return err
	}
	for _, component := range manifest.Components {
		actual, err := inspectInstallComponent(component.Name, component.Path)
		if err != nil || actual.Size != component.Size || !strings.EqualFold(actual.SHA256, component.SHA256) {
			return fmt.Errorf("%w: component %q changed", ErrInstallManifestInvalid, component.Name)
		}
	}
	if manifest.SchemaVersion == 1 {
		// Legacy manifests did not retain a Maintenance hash. Require a trusted
		// matching publisher before accepting that component into the v2 baseline.
		for _, component := range manifest.Components {
			if component.Name != "service" {
				continue
			}
			path := filepath.Join(filepath.Dir(component.Path), "desktop-guard-maintenance.exe")
			identity, err := verifySignature(path)
			if err != nil || !strings.EqualFold(identity.SHA256, manifest.SignerSHA256) {
				return fmt.Errorf("%w: legacy maintenance publisher verification failed", ErrInstallManifestInvalid)
			}
		}
	}
	return verifyRuntimeFiles(filepath.Dir(manifest.Components[0].Path), manifest.RuntimeFiles)
}

func validateInstallManifest(manifest InstallManifest) error {
	if (manifest.SchemaVersion != 1 && manifest.SchemaVersion != installManifestVersion) || !validNumericVersion(manifest.ProductVersion) || manifest.InstalledUTC.IsZero() || len(manifest.Components) != int(manifest.SchemaVersion)+2 || len(manifest.SignerSHA256) != sha256.Size*2 || manifest.SignerSubject == "" {
		return ErrInstallManifestInvalid
	}
	if _, err := hex.DecodeString(manifest.SignerSHA256); err != nil {
		return ErrInstallManifestInvalid
	}
	seen := make(map[string]struct{}, len(manifest.Components))
	for _, component := range manifest.Components {
		if component.Name == "" || !filepath.IsAbs(component.Path) || component.Size < 1 || len(component.SHA256) != sha256.Size*2 || component.Signature != "trusted" {
			return ErrInstallManifestInvalid
		}
		if _, err := hex.DecodeString(component.SHA256); err != nil {
			return ErrInstallManifestInvalid
		}
		if _, ok := seen[component.Name]; ok {
			return ErrInstallManifestInvalid
		}
		seen[component.Name] = struct{}{}
	}
	for _, name := range []string{"service", "ui", "agent"} {
		if _, ok := seen[name]; !ok {
			return ErrInstallManifestInvalid
		}
	}
	if manifest.SchemaVersion == installManifestVersion {
		if _, ok := seen["maintenance"]; !ok {
			return ErrInstallManifestInvalid
		}
	}
	return validateRuntimeRecords(manifest.RuntimeFiles)
}

func inspectInstallComponent(name, path string) (InstallComponentRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return InstallComponentRecord{}, fmt.Errorf("open install component %q: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		return InstallComponentRecord{}, fmt.Errorf("%w: component %q", ErrInstallManifestInvalid, name)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return InstallComponentRecord{}, fmt.Errorf("hash install component %q: %w", name, err)
	}
	return InstallComponentRecord{
		Name: name, Path: path, Size: info.Size(), SHA256: hex.EncodeToString(digest.Sum(nil)), Signature: "trusted",
	}, nil
}
