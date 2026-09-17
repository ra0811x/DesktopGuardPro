package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployWindowsVerifiesCopiesAndInstalls(t *testing.T) {
	source := createDeploySource(t)
	target := filepath.Join(t.TempDir(), "Desktop Guard Pro")
	var copied []string
	dependencies := successfulDeployerDependencies(&copied)
	result, err := deployWindows(DeployOptions{
		SourceDirectory: source, InstallDirectory: target, DataDirectory: filepath.Join(t.TempDir(), "data"), Version: "1.0.0",
	}, dependencies)
	if err != nil {
		t.Fatalf("deployWindows() error = %v", err)
	}
	if len(copied) != 4 || len(result.Copied) != 4 || result.Install.Options.Version != "1.0.0" {
		t.Fatalf("deploy result=%#v copied=%#v", result, copied)
	}
}

func TestDeployWindowsRejectsMixedSignerBeforeCreatingTarget(t *testing.T) {
	source := createDeploySource(t)
	target := filepath.Join(t.TempDir(), "Desktop Guard Pro")
	dependencies := successfulDeployerDependencies(nil)
	verificationCalls := 0
	dependencies.verifySignature = func(string) (SignatureIdentity, error) {
		verificationCalls++
		fingerprint := strings.Repeat("a", 64)
		if verificationCalls == 4 {
			fingerprint = strings.Repeat("b", 64)
		}
		return SignatureIdentity{SHA256: fingerprint, Subject: "Publisher"}, nil
	}
	_, err := deployWindows(DeployOptions{SourceDirectory: source, InstallDirectory: target, DataDirectory: filepath.Join(t.TempDir(), "data"), Version: "1.0.0"}, dependencies)
	if !errors.Is(err, ErrUpgradePublisherMismatch) {
		t.Fatalf("deployWindows() error = %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after rejected deploy: %v", statErr)
	}
}

func createDeploySource(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"desktop-guard-service.exe", "desktop-guard-ui.exe", "desktop-guard-agent.exe", "desktop-guard-maintenance.exe"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func successfulDeployerDependencies(copied *[]string) deployerDependencies {
	return deployerDependencies{
		isElevated:  func() bool { return true },
		trustTarget: func(string) error { return nil },
		verifySignature: func(string) (SignatureIdentity, error) {
			return SignatureIdentity{SHA256: strings.Repeat("a", 64), Subject: "Publisher"}, nil
		},
		copyFile: func(source, destination string) error {
			if copied != nil {
				*copied = append(*copied, destination)
			}
			content, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, content, 0o700)
		},
		install: func(options InstallOptions) (InstallResult, error) {
			return InstallResult{Options: ValidatedInstallOptions{Version: options.Version}}, nil
		},
	}
}
