package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"desktopguardpro/internal/maintenance"
)

type unavailableOutput struct{}

func (unavailableOutput) Write([]byte) (int, error) {
	return 0, errors.New("output handle is unavailable")
}

func TestRunDispatchesInstallAndWritesJSON(t *testing.T) {
	var got maintenance.InstallOptions
	operations := commandOperations{
		install: func(options maintenance.InstallOptions) (maintenance.InstallResult, error) {
			got = options
			return maintenance.InstallResult{OwnerUserSID: "S-1-5-21-1000"}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"install", "--install-dir", `C:\Program Files\Desktop Guard Pro`, "--data-dir", `C:\ProgramData\DesktopGuardPro`, "--owner-sid", "S-1-5-21-1000", "--allow-legacy-system-owner-migration", "--skip-service-health-check", "--version", "1.0.0"}, &stdout, &stderr, operations)
	if code != exitSuccess || got.Version != "1.0.0" || got.ServiceName != maintenance.DefaultServiceName || got.OwnerUserSID != "S-1-5-21-1000" || !got.AllowLegacySystemOwnerMigration || !got.SkipServiceHealthCheck {
		t.Fatalf("run() code=%d options=%#v stderr=%q", code, got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OwnerUserSID") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunDispatchesSignedDeploy(t *testing.T) {
	var got maintenance.DeployOptions
	operations := commandOperations{deploy: func(options maintenance.DeployOptions) (maintenance.DeployResult, error) {
		got = options
		return maintenance.DeployResult{}, nil
	}}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"deploy", "--source-dir", `C:\Release`, "--install-dir", `C:\Program Files\Desktop Guard Pro`,
		"--data-dir", `C:\ProgramData\DesktopGuardPro`, "--version", "1.0.0",
	}, &stdout, &stderr, operations)
	if code != exitSuccess || got.SourceDirectory != `C:\Release` || got.Version != "1.0.0" {
		t.Fatalf("run() code=%d options=%#v stderr=%q", code, got, stderr.String())
	}
}

func TestRunUsesStableExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		operations commandOperations
		want       int
	}{
		{name: "unknown", args: []string{"unknown"}, want: exitUsage},
		{name: "unsupported runtime", args: []string{"uninstall", "--data-dir", "D:\\CustomData"}, operations: commandOperations{uninstall: func(maintenance.UninstallOptions) (maintenance.UninstallResult, error) {
			return maintenance.UninstallResult{}, maintenance.ErrUnsupportedRuntimeConfiguration
		}}, want: exitUsage},
		{name: "active session", args: []string{"uninstall", "--data-dir", `C:\ProgramData\DesktopGuardPro`}, operations: commandOperations{uninstall: func(maintenance.UninstallOptions) (maintenance.UninstallResult, error) {
			return maintenance.UninstallResult{}, maintenance.ErrActiveProtectionSession
		}}, want: exitActiveSession},
		{name: "verification", args: []string{"verify", "--data-dir", `C:\ProgramData\DesktopGuardPro`}, operations: commandOperations{verify: func(string) (maintenance.VerificationResult, error) {
			return maintenance.VerificationResult{}, errors.New("installation verification failed")
		}}, want: exitVerification},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(test.args, &stdout, &stderr, test.operations); got != test.want {
				t.Fatalf("run() = %d, want %d; stderr=%q", got, test.want, stderr.String())
			}
		})
	}
}

func TestRunPassesMSIUninstallOwnerToAuthorization(t *testing.T) {
	var got maintenance.UninstallOptions
	operations := commandOperations{uninstall: func(options maintenance.UninstallOptions) (maintenance.UninstallResult, error) {
		got = options
		return maintenance.UninstallResult{}, nil
	}}
	var stdout, stderr bytes.Buffer
	code := run([]string{"uninstall", "--data-dir", `C:\ProgramData\DesktopGuardPro`, "--owner-sid", "S-1-5-21-1000-1001-1002-1003", "--msi-managed"}, &stdout, &stderr, operations)
	if code != exitSuccess || got.OwnerUserSID != "S-1-5-21-1000-1001-1002-1003" || got.Disposition != maintenance.DataDispositionPreserve || !got.ManagedByMSI {
		t.Fatalf("code=%d options=%+v stderr=%s", code, got, stderr.String())
	}
}

func TestRunDispatchesMSITransactionStages(t *testing.T) {
	for _, stage := range []string{"prepare", "apply", "stop-rollback", "rollback", "commit"} {
		operations := commandOperations{msi: func(gotStage string, options maintenance.MSIOptions) (maintenance.MSIResult, error) {
			if gotStage != stage || options.TransactionID != "{1234}" || options.OwnerUserSID != "S-1-5-21-1000" || options.Version != "1.2.3" {
				t.Fatalf("MSI args: %s, %+v", gotStage, options)
			}
			return maintenance.MSIResult{Stage: stage}, nil
		}}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"msi", "--stage", stage, "--install-dir", `C:\Program Files\Desktop Guard Pro`, "--data-dir", `C:\ProgramData\DesktopGuardPro`, "--owner-sid", "S-1-5-21-1000", "--transaction-id", "{1234}", "--version", "1.2.3"}, &stdout, &stderr, operations); code != exitSuccess {
			t.Fatalf("MSI %s code=%d, %s", stage, code, stderr.String())
		}
	}
}

func TestRunKeepsSuccessfulOperationWhenGUIOutputIsUnavailable(t *testing.T) {
	called := false
	operations := commandOperations{msi: func(stage string, options maintenance.MSIOptions) (maintenance.MSIResult, error) {
		called = true
		return maintenance.MSIResult{Stage: stage, TransactionID: options.TransactionID}, nil
	}}
	var stderr bytes.Buffer
	code := run([]string{
		"msi", "--stage", "prepare", "--install-dir", `C:\Program Files\Desktop Guard Pro`,
		"--data-dir", `C:\ProgramData\DesktopGuardPro`, "--owner-sid", "S-1-5-21-1000",
		"--transaction-id", "{1234}", "--version", "1.2.3",
	}, unavailableOutput{}, &stderr, operations)
	if code != exitSuccess || !called {
		t.Fatalf("run() code=%d called=%v stderr=%q", code, called, stderr.String())
	}
}
