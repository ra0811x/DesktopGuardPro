package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAndSaveInstallManifest(t *testing.T) {
	installDirectory := t.TempDir()
	options := validInstallOptions(t, installDirectory, filepath.Join(filepath.Dir(installDirectory), filepath.Base(installDirectory)+"-data"))
	validated, err := ValidateInstallOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	installedUTC := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	manifest, err := BuildInstallManifest(validated, PreflightReport{
		WindowsBuild: 26100, WebView2Version: "151.0.4129.101",
		SignerSHA256: strings.Repeat("a", 64), SignerSubject: "Desktop Guard Pro Test",
	}, installedUTC)
	if err != nil {
		t.Fatalf("BuildInstallManifest() error = %v", err)
	}
	if manifest.SchemaVersion != installManifestVersion || len(manifest.Components) != 4 || manifest.InstalledUTC.Location() != time.UTC {
		t.Fatalf("manifest = %#v", manifest)
	}
	wantDigest := sha256.Sum256([]byte("component"))
	if manifest.Components[0].SHA256 != hex.EncodeToString(wantDigest[:]) || manifest.Components[0].Signature != "trusted" {
		t.Fatalf("component record = %#v", manifest.Components[0])
	}

	dataDirectory := t.TempDir()
	if err := SaveInstallManifest(dataDirectory, manifest); err != nil {
		t.Fatalf("SaveInstallManifest() error = %v", err)
	}
	encoded, err := os.ReadFile(filepath.Join(dataDirectory, InstallManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var saved InstallManifest
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatalf("decode saved manifest: %v", err)
	}
	if saved.ProductVersion != validated.Version || saved.WindowsBuild != 26100 || saved.SignerSHA256 != strings.Repeat("a", 64) || len(saved.Components) != 4 {
		t.Fatalf("saved manifest = %#v", saved)
	}
}

func TestBuildInstallManifestRejectsEmptyComponent(t *testing.T) {
	installDirectory := t.TempDir()
	path := filepath.Join(installDirectory, "service.exe")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := BuildInstallManifest(ValidatedInstallOptions{
		Version: "1.0.0", ServiceExecutable: path, UIExecutable: path, AgentExecutable: path,
	}, PreflightReport{}, time.Now())
	if err == nil {
		t.Fatal("BuildInstallManifest() error = nil, want failure")
	}
}

func TestLoadAndVerifyInstallManifestDetectsComponentTampering(t *testing.T) {
	installDirectory := t.TempDir()
	options := validInstallOptions(t, installDirectory, filepath.Join(filepath.Dir(installDirectory), filepath.Base(installDirectory)+"-data"))
	validated, err := ValidateInstallOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildInstallManifest(validated, PreflightReport{
		WindowsBuild: 26100, WebView2Version: "151.0.4129.101",
		SignerSHA256: strings.Repeat("a", 64), SignerSubject: "Desktop Guard Pro Test",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	dataDirectory := t.TempDir()
	if err := SaveInstallManifest(dataDirectory, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInstallManifest(dataDirectory)
	if err != nil || VerifyInstalledComponents(loaded) != nil {
		t.Fatalf("load or verify manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(validated.InstallDirectory, "desktop-guard-maintenance.exe"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalledComponents(loaded); err == nil {
		t.Fatal("VerifyInstalledComponents() error = nil after tampering")
	}
}

func TestLegacyManifestRequiresMaintenancePublisherVerification(t *testing.T) {
	directory := t.TempDir()
	options, err := ValidateInstallOptions(validInstallOptions(t, directory, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildInstallManifest(options, PreflightReport{SignerSHA256: strings.Repeat("a", 64), SignerSubject: "Publisher"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion, manifest.Components = 1, manifest.Components[:3]
	called := false
	verify := func(path string) (SignatureIdentity, error) {
		called = true
		if filepath.Base(path) != "desktop-guard-maintenance.exe" {
			t.Fatalf("wrong legacy component: %s", path)
		}
		return SignatureIdentity{SHA256: manifest.SignerSHA256}, nil
	}
	if err := verifyInstalledComponents(manifest, verify); err != nil || !called {
		t.Fatalf("legacy verification: %v, called=%v", err, called)
	}
	if err := verifyInstalledComponents(manifest, func(string) (SignatureIdentity, error) {
		return SignatureIdentity{SHA256: strings.Repeat("b", 64)}, nil
	}); err == nil {
		t.Fatal("legacy maintenance publisher mismatch accepted")
	}
}
