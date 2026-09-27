package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInstallOptions(t *testing.T) {
	installDirectory := t.TempDir()
	dataDirectory := filepath.Join(filepath.Dir(installDirectory), filepath.Base(installDirectory)+"-data")
	options := validInstallOptions(t, installDirectory, dataDirectory)

	validated, err := ValidateInstallOptions(options)
	if err != nil {
		t.Fatalf("ValidateInstallOptions() error = %v", err)
	}
	if validated.ServiceName != DefaultServiceName || validated.Version != options.Version {
		t.Fatalf("validated identity = %q %q", validated.ServiceName, validated.Version)
	}
	if validated.InstallDirectory != filepath.Clean(installDirectory) || validated.DataDirectory != filepath.Clean(dataDirectory) {
		t.Fatalf("validated directories = %q %q", validated.InstallDirectory, validated.DataDirectory)
	}
}

func TestValidateInstallOptionsRejectsUnsafeLayouts(t *testing.T) {
	installDirectory := t.TempDir()
	dataDirectory := filepath.Join(filepath.Dir(installDirectory), filepath.Base(installDirectory)+"-data")

	tests := []struct {
		name    string
		mutate  func(*InstallOptions)
		wantErr error
	}{
		{name: "relative install directory", mutate: func(options *InstallOptions) { options.InstallDirectory = "." }, wantErr: ErrInstallPathInvalid},
		{name: "data inside install directory", mutate: func(options *InstallOptions) { options.DataDirectory = filepath.Join(installDirectory, "data") }, wantErr: ErrInstallLayoutInvalid},
		{name: "component outside install directory", mutate: func(options *InstallOptions) {
			options.UIExecutable = filepath.Join(t.TempDir(), "ui.exe")
			writeComponent(t, options.UIExecutable)
		}, wantErr: ErrComponentInvalid},
		{name: "duplicate component", mutate: func(options *InstallOptions) { options.AgentExecutable = options.UIExecutable }, wantErr: ErrInstallLayoutInvalid},
		{name: "non executable component", mutate: func(options *InstallOptions) {
			options.AgentExecutable = filepath.Join(installDirectory, "agent.bin")
			writeComponent(t, options.AgentExecutable)
		}, wantErr: ErrComponentInvalid},
		{name: "invalid service name", mutate: func(options *InstallOptions) { options.ServiceName = `Desktop\\Guard` }, wantErr: ErrServiceNameInvalid},
		{name: "invalid version", mutate: func(options *InstallOptions) { options.Version = "1.0-beta" }, wantErr: ErrVersionInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validInstallOptions(t, installDirectory, dataDirectory)
			test.mutate(&options)
			_, err := ValidateInstallOptions(options)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateInstallOptions() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateInstallOptionsRejectsMissingComponent(t *testing.T) {
	installDirectory := t.TempDir()
	dataDirectory := filepath.Join(filepath.Dir(installDirectory), filepath.Base(installDirectory)+"-data")
	options := validInstallOptions(t, installDirectory, dataDirectory)
	if err := os.Remove(options.AgentExecutable); err != nil {
		t.Fatalf("remove agent executable: %v", err)
	}

	_, err := ValidateInstallOptions(options)
	if !errors.Is(err, ErrComponentInvalid) {
		t.Fatalf("ValidateInstallOptions() error = %v, want %v", err, ErrComponentInvalid)
	}
}

func validInstallOptions(t *testing.T, installDirectory, dataDirectory string) InstallOptions {
	t.Helper()
	options := InstallOptions{
		InstallDirectory:  installDirectory,
		DataDirectory:     dataDirectory,
		ServiceExecutable: filepath.Join(installDirectory, "desktop-guard-service.exe"),
		UIExecutable:      filepath.Join(installDirectory, "desktop-guard-ui.exe"),
		AgentExecutable:   filepath.Join(installDirectory, "desktop-guard-agent.exe"),
		Version:           "1.0.0",
	}
	writeComponent(t, options.ServiceExecutable)
	writeComponent(t, options.UIExecutable)
	writeComponent(t, options.AgentExecutable)
	writeComponent(t, filepath.Join(installDirectory, "desktop-guard-maintenance.exe"))
	return options
}

func TestInstallValidationRequiresMaintenanceComponent(t *testing.T) {
	directory := t.TempDir()
	options := validInstallOptions(t, directory, t.TempDir())
	if err := os.Remove(filepath.Join(directory, "desktop-guard-maintenance.exe")); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateInstallOptions(options); !errors.Is(err, ErrComponentInvalid) {
		t.Fatalf("missing maintenance accepted: %v", err)
	}
}

func writeComponent(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("component"), 0o600); err != nil {
		t.Fatalf("write component %q: %v", path, err)
	}
}
