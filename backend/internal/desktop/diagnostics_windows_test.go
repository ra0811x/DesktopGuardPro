package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteStartupFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "logs", "desktop-ui.log")
	at := time.Date(2026, 8, 23, 10, 11, 12, 0, time.UTC)
	if err := WriteStartupFailure(path, errors.New("webview initialization failed\nHRESULT 0x80004005"), at); err != nil {
		t.Fatalf("WriteStartupFailure() error = %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(contents)
	if !strings.Contains(got, "2026-08-23T10:11:12Z") ||
		!strings.Contains(got, "webview initialization failed | HRESULT 0x80004005") {
		t.Fatalf("startup log = %q", got)
	}
}

func TestStartupFailureMessageIncludesRecoveryAndLogPath(t *testing.T) {
	t.Parallel()

	message := StartupFailureMessage(`C:\Users\Raymond\AppData\Local\DesktopGuardPro\logs\desktop-ui.log`)
	if !strings.Contains(message, "WebView2 Runtime") {
		t.Fatalf("message does not mention WebView2 Runtime: %q", message)
	}
	if !strings.Contains(message, "desktop-ui.log") {
		t.Fatalf("message does not include log path: %q", message)
	}
}

func TestSelectWebView2VersionSkipsMissingAndZeroVersions(t *testing.T) {
	t.Parallel()

	version, err := selectWebView2Version(
		func() (string, error) { return "", os.ErrNotExist },
		func() (string, error) { return "0.0.0.0", nil },
		func() (string, error) { return "151.0.4129.101", nil },
	)
	if err != nil {
		t.Fatalf("selectWebView2Version() error = %v", err)
	}
	if version != "151.0.4129.101" {
		t.Fatalf("selectWebView2Version() = %q, want %q", version, "151.0.4129.101")
	}
}

func TestSelectWebView2VersionReportsUnavailableRuntime(t *testing.T) {
	t.Parallel()

	_, err := selectWebView2Version(
		func() (string, error) { return "", os.ErrNotExist },
		func() (string, error) { return "0.0.0.0", nil },
	)
	if !errors.Is(err, ErrWebView2RuntimeUnavailable) {
		t.Fatalf("selectWebView2Version() error = %v, want %v", err, ErrWebView2RuntimeUnavailable)
	}
}
