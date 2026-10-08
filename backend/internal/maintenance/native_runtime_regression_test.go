package maintenance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegressionDeployIncludesNativeUIAssembly(t *testing.T) {
	source := createDeploySource(t)
	if err := os.WriteFile(filepath.Join(source, "desktop-guard-ui.dll"), []byte("review native assembly"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "Desktop Guard Pro")
	result, err := deployWindows(DeployOptions{SourceDirectory: source, InstallDirectory: target, DataDirectory: filepath.Join(t.TempDir(), "data"), Version: "2.11.41"}, successfulDeployerDependencies(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "desktop-guard-ui.dll")); err != nil {
		t.Fatalf("deploy succeeds without native UI assembly: copied=%v err=%v", result.Copied, err)
	}
}

func writeNativeRuntimeFixture(t *testing.T, directory string, files map[string]string) []RuntimeFileRecord {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	records, err := CollectRuntimeFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		RuntimeFiles []RuntimeFileRecord `json:"runtimeFiles"`
	}{records})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "release-manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestNativeDeploymentCopiesNestedAndEmptyResources(t *testing.T) {
	source := createDeploySource(t)
	files := map[string]string{"desktop-guard-ui.dll": "assembly", "desktop-guard-ui.runtimeconfig.json": "config", "Assets/theme.xaml": "theme", "Assets/empty.txt": ""}
	writeNativeRuntimeFixture(t, source, files)
	target := filepath.Join(t.TempDir(), "App")
	_, err := deployWindows(DeployOptions{SourceDirectory: source, InstallDirectory: target, Version: "2.11.41"}, successfulDeployerDependencies(nil))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		assertFileContent(t, filepath.Join(target, filepath.FromSlash(name)), content)
	}
	if _, err := os.Stat(filepath.Join(target, "release-manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRuntimeInventoryRejectsTraversalAndTampering(t *testing.T) {
	directory := createDeploySource(t)
	for _, name := range []string{"../outside.dll", "C:/outside.dll", "desktop-guard-ui.exe", "Assets/../outside.dll", "Assets/file:stream"} {
		if err := validateRuntimeRecords([]RuntimeFileRecord{{Path: name, Size: 1, SHA256: strings.Repeat("a", 64)}}); err == nil {
			t.Fatalf("unsafe runtime name accepted: %s", name)
		}
	}
	writeNativeRuntimeFixture(t, directory, map[string]string{"desktop-guard-ui.dll": "assembly"})
	if err := os.WriteFile(filepath.Join(directory, "desktop-guard-ui.dll"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReleaseRuntimeFiles(directory, true); err == nil {
		t.Fatal("changed assembly accepted")
	}
}

func TestNativeDeploymentRejectsChangedCopiedAssemblyBeforeInstallation(t *testing.T) {
	source := createDeploySource(t)
	writeNativeRuntimeFixture(t, source, map[string]string{"desktop-guard-ui.dll": "assembly"})
	deps := successfulDeployerDependencies(nil)
	copyFile := deps.copyFile
	deps.copyFile = func(source, target string) error {
		if err := copyFile(source, target); err != nil {
			return err
		}
		if filepath.Base(target) == "desktop-guard-ui.dll" {
			return os.WriteFile(target, []byte("changed assembly"), 0600)
		}
		return nil
	}
	installed := false
	deps.install = func(InstallOptions) (InstallResult, error) { installed = true; return InstallResult{}, nil }
	_, err := deployWindows(DeployOptions{SourceDirectory: source, InstallDirectory: filepath.Join(t.TempDir(), "App"), Version: "2.11.41"}, deps)
	if err == nil || installed {
		t.Fatalf("changed runtime reached installation: err=%v installed=%t", err, installed)
	}
}

func TestNativeUpgradeAndRollbackRestoreCompleteFileSet(t *testing.T) {
	for _, failHealth := range []bool{false, true} {
		t.Run(fmtBool(failHealth), func(t *testing.T) {
			current, staged, data := createDeploySource(t), createDeploySource(t), t.TempDir()
			old := writeNativeRuntimeFixture(t, current, map[string]string{"desktop-guard-ui.dll": "old assembly", "obsolete.xaml": "old resource"})
			if err := os.WriteFile(filepath.Join(current, "untracked.txt"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			newFiles := map[string]string{"desktop-guard-ui.dll": "new assembly", "Assets/new/theme.xaml": "new theme", "Assets/empty.txt": ""}
			writeNativeRuntimeFixture(t, staged, newFiles)
			options := ValidatedUpgradeOptions{
				Current: ValidatedInstallOptions{InstallDirectory: current, DataDirectory: data, Version: "1.0.0", ServiceExecutable: filepath.Join(current, "desktop-guard-service.exe"), UIExecutable: filepath.Join(current, "desktop-guard-ui.exe"), AgentExecutable: filepath.Join(current, "desktop-guard-agent.exe")},
				Staged:  ValidatedInstallOptions{InstallDirectory: staged, Version: "1.1.0", ServiceExecutable: filepath.Join(staged, "desktop-guard-service.exe"), UIExecutable: filepath.Join(staged, "desktop-guard-ui.exe"), AgentExecutable: filepath.Join(staged, "desktop-guard-agent.exe")},
			}
			var calls []string
			deps, _ := successfulUpgraderDependencies(&calls)
			deps.validate = func(UpgradeOptions) (ValidatedUpgradeOptions, error) { return options, nil }
			deps.loadManifest = func(string) (InstallManifest, error) {
				return InstallManifest{ProductVersion: "1.0.0", SignerSHA256: strings.Repeat("a", 64), RuntimeFiles: old}, nil
			}
			deps.runtimeReplacements = prepareRuntimeReplacements
			deps.prepareSwap = func(pairs []ComponentReplacement) (componentSwapTransaction, error) {
				return PrepareComponentSwap(pairs)
			}
			deps.buildManifest = BuildInstallManifest
			deps.preflight = func(ValidatedInstallOptions) (PreflightReport, error) {
				return PreflightReport{SignerSHA256: strings.Repeat("a", 64), SignerSubject: "Publisher"}, nil
			}
			healthCalls := 0
			deps.waitForHealth = func() error {
				healthCalls++
				if failHealth && healthCalls == 1 {
					return errors.New("health failed")
				}
				return nil
			}
			var saved InstallManifest
			deps.saveManifest = func(_ string, manifest InstallManifest) error { saved = manifest; return nil }
			_, err := upgradeWindows(UpgradeOptions{}, deps)
			assertFileContent(t, filepath.Join(current, "untracked.txt"), "keep")
			if failHealth {
				if err == nil {
					t.Fatal("health failure ignored")
				}
				assertFileContent(t, filepath.Join(current, "desktop-guard-ui.dll"), "old assembly")
				assertFileContent(t, filepath.Join(current, "obsolete.xaml"), "old resource")
				if _, err := os.Stat(filepath.Join(current, "Assets")); !os.IsNotExist(err) {
					t.Fatalf("new resources remain after rollback: %v", err)
				}
				if _, err := readReleaseRuntimeFiles(current, false); err != nil {
					t.Fatalf("old release manifest not restored: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for name, content := range newFiles {
					assertFileContent(t, filepath.Join(current, filepath.FromSlash(name)), content)
				}
				if _, err := os.Stat(filepath.Join(current, "obsolete.xaml")); !os.IsNotExist(err) {
					t.Fatalf("obsolete resource remains: %v", err)
				}
				if len(saved.RuntimeFiles) != len(newFiles) || verifyRuntimeFiles(current, saved.RuntimeFiles) != nil {
					t.Fatalf("new installation inventory = %+v", saved.RuntimeFiles)
				}
			}
		})
	}
}

func fmtBool(value bool) string {
	if value {
		return "rollback"
	}
	return "commit"
}

func TestNativeInstallManifestVerifiesRuntimeAndLoadsLegacyManifest(t *testing.T) {
	directory, data := t.TempDir(), t.TempDir()
	options, err := ValidateInstallOptions(validInstallOptions(t, directory, data))
	if err != nil {
		t.Fatal(err)
	}
	writeNativeRuntimeFixture(t, directory, map[string]string{"desktop-guard-ui.dll": "assembly", "Assets/theme.xaml": "theme"})
	manifest, err := BuildInstallManifest(options, PreflightReport{SignerSHA256: strings.Repeat("a", 64), SignerSubject: "Publisher"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveInstallManifest(data, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInstallManifest(data)
	if err != nil || VerifyInstalledComponents(loaded) != nil {
		t.Fatalf("new manifest verification: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "desktop-guard-ui.dll"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyInstalledComponents(loaded); err == nil {
		t.Fatal("assembly tampering ignored")
	}
	loaded.RuntimeFiles = nil
	if err := SaveInstallManifest(data, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInstallManifest(data); err != nil {
		t.Fatalf("legacy manifest without runtime files rejected: %v", err)
	}
}
