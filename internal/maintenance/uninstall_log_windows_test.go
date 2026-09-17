package maintenance

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestWriteUninstallResultLogCreatesExclusiveJSON(t *testing.T) {
	result := UninstallResult{
		CompletedUTC: time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC),
		Disposition:  DataDispositionPreserve, DataPreserved: true,
	}
	path, err := writeUninstallResultLog(t.TempDir(), result, "0123456789abcdef")
	if err != nil {
		t.Fatalf("writeUninstallResultLog() error = %v", err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document uninstallLogDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.Result.Disposition != DataDispositionPreserve || !document.Result.DataPreserved {
		t.Fatalf("uninstall log = %#v", document)
	}
	if _, err := writeUninstallResultLog(t.TempDir(), UninstallResult{}, "id"); err != ErrUninstallLogInvalid {
		t.Fatalf("invalid log error = %v, want %v", err, ErrUninstallLogInvalid)
	}
}
