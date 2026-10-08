package maintenance

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleInstalledProgramRemovalUsesManifestDirectoryOnly(t *testing.T) {
	directory := `C:\Program Files\Desktop Guard Pro`
	manifest := removalTestManifest(directory)
	var scheduled []string
	err := scheduleInstalledProgramRemoval(manifest, func(path string) error {
		if !samePath(path, directory) {
			t.Fatalf("trusted directory = %q", path)
		}
		return nil
	}, func(path string) error {
		scheduled = append(scheduled, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scheduled) != 6 || !samePath(scheduled[len(scheduled)-1], directory) || filepath.Base(scheduled[3]) != "desktop-guard-maintenance.exe" {
		t.Fatalf("scheduled paths = %#v", scheduled)
	}
}

func TestScheduleInstalledProgramRemovalRejectsSplitDirectories(t *testing.T) {
	manifest := removalTestManifest(`C:\Program Files\Desktop Guard Pro`)
	manifest.Components[2].Path = `C:\Other\desktop-guard-agent.exe`
	err := scheduleInstalledProgramRemoval(manifest, func(string) error { return nil }, func(string) error { return nil })
	if !errors.Is(err, ErrInstallManifestInvalid) {
		t.Fatalf("scheduleInstalledProgramRemoval() error = %v", err)
	}
}

func TestFilterPendingProgramRemovalsKeepsUnrelatedOperations(t *testing.T) {
	installDirectory := `C:\Program Files\Desktop Guard Pro`
	values := []string{
		`\??\C:\Program Files\Desktop Guard Pro\desktop-guard-ui.dll`, "",
		`\??\C:\Program Files\Desktop Guard Pro\Assets\theme.xaml`, "",
		`\??\C:\Program Files\Desktop Guard Pro\desktop-guard-service.exe`, "",
		`\??\C:\Other App\other.exe`, "",
		`\??\C:\Temp\old.txt`, `\??\C:\Temp\new.txt`,
		`\??\C:\Program Files\Desktop Guard Pro`, "",
	}
	filtered, changed, err := filterPendingProgramRemovals(values, installedProgramPaths(installDirectory))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`\??\C:\Other App\other.exe`, "",
		`\??\C:\Temp\old.txt`, `\??\C:\Temp\new.txt`,
	}
	if !changed || len(filtered) != len(want) {
		t.Fatalf("filtered=%#v changed=%v", filtered, changed)
	}
	for index := range want {
		if filtered[index] != want[index] {
			t.Fatalf("filtered[%d]=%q, want %q", index, filtered[index], want[index])
		}
	}
}

func removalTestManifest(directory string) InstallManifest {
	digest := strings.Repeat("a", 64)
	return InstallManifest{
		SchemaVersion: installManifestVersion, ProductVersion: "1.0.0", InstalledUTC: time.Now().UTC(),
		SignerSHA256: digest, SignerSubject: "Publisher",
		Components: []InstallComponentRecord{
			{Name: "service", Path: filepath.Join(directory, "desktop-guard-service.exe"), Size: 1, SHA256: digest, Signature: "trusted"},
			{Name: "ui", Path: filepath.Join(directory, "desktop-guard-ui.exe"), Size: 1, SHA256: digest, Signature: "trusted"},
			{Name: "agent", Path: filepath.Join(directory, "desktop-guard-agent.exe"), Size: 1, SHA256: digest, Signature: "trusted"},
			{Name: "maintenance", Path: filepath.Join(directory, "desktop-guard-maintenance.exe"), Size: 1, SHA256: digest, Signature: "trusted"},
		},
	}
}
