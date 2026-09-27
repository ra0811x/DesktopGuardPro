package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUpgradeOptionsAcceptsStrictVersionIncrease(t *testing.T) {
	root := t.TempDir()
	installDirectory := filepath.Join(root, "current")
	stagedDirectory := filepath.Join(root, "staged")
	if err := createUpgradeComponents(t, installDirectory); err != nil {
		t.Fatal(err)
	}
	if err := createUpgradeComponents(t, stagedDirectory); err != nil {
		t.Fatal(err)
	}
	options := UpgradeOptions{
		InstallDirectory: installDirectory, StagedDirectory: stagedDirectory,
		DataDirectory: filepath.Join(root, "data"), CurrentVersion: "1.2.3", TargetVersion: "1.3.0",
	}
	validated, err := ValidateUpgradeOptions(options)
	if err != nil {
		t.Fatalf("ValidateUpgradeOptions() error = %v", err)
	}
	if validated.Current.Version != "1.2.3" || validated.Staged.Version != "1.3.0" {
		t.Fatalf("validated upgrade = %#v", validated)
	}
}

func TestValidateUpgradeOptionsRejectsNonIncreasingAndOverlappingLayouts(t *testing.T) {
	root := t.TempDir()
	installDirectory := filepath.Join(root, "current")
	stagedDirectory := filepath.Join(root, "staged")
	_ = createUpgradeComponents(t, installDirectory)
	_ = createUpgradeComponents(t, stagedDirectory)
	base := UpgradeOptions{
		InstallDirectory: installDirectory, StagedDirectory: stagedDirectory,
		DataDirectory: filepath.Join(root, "data"), CurrentVersion: "1.2.3", TargetVersion: "1.3.0",
	}
	tests := []struct {
		name   string
		mutate func(*UpgradeOptions)
	}{
		{name: "same version", mutate: func(options *UpgradeOptions) { options.TargetVersion = options.CurrentVersion }},
		{name: "downgrade", mutate: func(options *UpgradeOptions) { options.TargetVersion = "1.2.2" }},
		{name: "same directory", mutate: func(options *UpgradeOptions) { options.StagedDirectory = options.InstallDirectory }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.mutate(&options)
			_, err := ValidateUpgradeOptions(options)
			if !errors.Is(err, ErrUpgradeOptionsInvalid) {
				t.Fatalf("ValidateUpgradeOptions() error = %v", err)
			}
		})
	}
}

func createUpgradeComponents(t *testing.T, directory string) error {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"desktop-guard-service.exe", "desktop-guard-ui.exe", "desktop-guard-agent.exe", "desktop-guard-maintenance.exe"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o700); err != nil {
			return err
		}
	}
	return nil
}
