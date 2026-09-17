package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUninstallOptionsSupportsThreeDataDispositions(t *testing.T) {
	dataDirectory := t.TempDir()
	archive := filepath.Join(t.TempDir(), "audit-export.zip")
	if err := os.WriteFile(archive, []byte("export"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []UninstallOptions{
		{DataDirectory: dataDirectory, Disposition: DataDispositionPreserve},
		{DataDirectory: dataDirectory, Disposition: DataDispositionExportAndDelete, ExportArchivePath: archive, DeletionConfirmation: "DELETE owner"},
		{DataDirectory: dataDirectory, Disposition: DataDispositionDeleteNow, DeletionConfirmation: "DELETE owner"},
	}
	for _, options := range tests {
		validated, err := ValidateUninstallOptions(options, dataDirectory)
		if err != nil {
			t.Fatalf("ValidateUninstallOptions(%q) error = %v", options.Disposition, err)
		}
		if validated.ServiceName != DefaultServiceName || validated.Disposition != options.Disposition {
			t.Fatalf("validated options = %#v", validated)
		}
	}
}

func TestValidateUninstallOptionsRejectsUnsafeDeletion(t *testing.T) {
	dataDirectory := t.TempDir()
	archiveInside := filepath.Join(dataDirectory, "export.zip")
	if err := os.WriteFile(archiveInside, []byte("export"), 0o600); err != nil {
		t.Fatal(err)
	}
	archiveOutside := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(archiveOutside, []byte("export"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		options UninstallOptions
		wantErr error
	}{
		{name: "unexpected data path", options: UninstallOptions{DataDirectory: dataDirectory, Disposition: DataDispositionPreserve}, wantErr: ErrUninstallOptionsInvalid},
		{name: "missing delete confirmation", options: UninstallOptions{DataDirectory: dataDirectory, Disposition: DataDispositionDeleteNow}, wantErr: ErrDataDeletionUnconfirmed},
		{name: "missing export deletion confirmation", options: UninstallOptions{DataDirectory: dataDirectory, Disposition: DataDispositionExportAndDelete, ExportArchivePath: archiveOutside}, wantErr: ErrDataDeletionUnconfirmed},
		{name: "archive inside data", options: UninstallOptions{DataDirectory: dataDirectory, Disposition: DataDispositionExportAndDelete, ExportArchivePath: archiveInside, DeletionConfirmation: "DELETE owner"}, wantErr: ErrUninstallOptionsInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := dataDirectory
			if test.name == "unexpected data path" {
				expected = t.TempDir()
			}
			_, err := ValidateUninstallOptions(test.options, expected)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateUninstallOptions() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
