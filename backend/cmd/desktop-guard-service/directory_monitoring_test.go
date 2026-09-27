package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
)

func TestApplicationRuntimeCollectsConfiguredDirectoryChangesIntoTimelineAndReports(t *testing.T) {
	dataDirectory, watched := t.TempDir(), t.TempDir()
	runtime, err := openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if runtime != nil {
			_ = runtime.Close()
		}
	}()
	policy := domain.DefaultMonitoringPolicy()
	policy.ProcessAndSoftwareEnabled = false
	policy.SystemAndNetworkEnabled = false
	policy.ExternalDevicesEnabled = false
	policy.UserSessionActivityEnabled = false
	callRuntimeAPI(t, runtime, contracts.MessageTypeDirectoryMonitoringUpdate, coreservice.DirectoryMonitoringResult{
		Directories: []string{watched},
	})
	callRuntimeAPI(t, runtime, contracts.MessageTypeDirectoryMonitoringUpdate, coreservice.DirectoryMonitoringResult{
		MonitoringPolicy: &policy,
	})
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = openTestApplicationRuntime(context.Background(), dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	var settings coreservice.DirectoryMonitoringResult
	if err := callRuntimeAPI(t, runtime, contracts.MessageTypeDirectoryMonitoringGet, struct{}{}).DecodePayload(&settings); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings.Directories, []string{watched}) {
		t.Fatalf("recovered directory configuration = %v", settings.Directories)
	}
	nested := filepath.Join(watched, "子目录")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	done := make(chan error, 1)
	go func() { done <- runtime.collectors.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("collector shutdown: %v", err)
		}
	}()
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionCreate, coreservice.CreateSessionRequest{
		ID: "file-session", Name: "重点目录测试", MonitoringMode: domain.MonitoringModeStandard,
	})
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive} {
		callRuntimeAPI(t, runtime, contracts.MessageTypeSessionTransition, coreservice.TransitionSessionRequest{State: state})
	}

	waitForFile := func(action, path string, after uint64) uint64 {
		t.Helper()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			response := callRuntimeAPI(t, runtime, contracts.MessageTypeTimelineQuery, coreservice.TimelineQueryRequest{
				SessionID: "file-session", Categories: []domain.EventCategory{domain.EventCategoryFile}, Limit: 100,
			})
			var page storage.TimelinePage
			if err := response.DecodePayload(&page); err != nil {
				t.Fatal(err)
			}
			if !page.IntegrityVerified {
				t.Fatal("file timeline did not verify integrity")
			}
			for _, record := range page.Records {
				if record.Event.Action == action && record.Event.ObjectKey == path && record.Event.Sequence > after {
					return record.Event.Sequence
				}
			}
			select {
			case <-ctx.Done():
				t.Fatalf("production pipeline did not record %s for %s", action, path)
			case <-ticker.C:
			}
		}
	}
	path := filepath.Join(nested, "原始文件.txt")
	if err := os.WriteFile(path, []byte("created"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := waitForFile("file_created", path, 0)
	modified := waitForFile("file_modified", path, created)
	if err := os.WriteFile(path, []byte("modified contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFile("file_modified", path, modified)
	renamed := filepath.Join(nested, "重命名文件.txt")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	waitForFile("file_renamed", renamed, 0)
	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	waitForFile("file_deleted", renamed, 0)
	runtime.api.SetSystemCredentialVerifier(directoryTestCredentialsVerifier{})
	var challenge coreservice.EndVerificationChallenge
	if err := callRuntimeAPI(t, runtime, contracts.MessageTypeSessionEndVerificationCreate, struct{}{}).DecodePayload(&challenge); err != nil {
		t.Fatal(err)
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionTransition, coreservice.TransitionSessionRequest{
		State:        domain.SessionStateFinalizing,
		Verification: &coreservice.EndProtectionVerification{Token: challenge.Token, UserName: "test-owner", Password: []byte("test-only")},
	})
	callRuntimeAPI(t, runtime, contracts.MessageTypeSessionTransition, coreservice.TransitionSessionRequest{State: domain.SessionStateCompleted})
	for _, format := range []coreservice.ReportFormat{coreservice.ReportFormatHTML, coreservice.ReportFormatMarkdown} {
		response := callRuntimeAPI(t, runtime, contracts.MessageTypeReportExport, coreservice.ReportExportRequest{
			SessionID: "file-session", Format: format,
		})
		var report coreservice.ReportResult
		if err := response.DecodePayload(&report); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"file_created", "file_modified", "file_renamed", "file_deleted"} {
			if !strings.Contains(report.Content, action) {
				t.Errorf("%s report is missing %s", format, action)
			}
		}
	}
}

type directoryTestCredentialsVerifier struct{}

func (directoryTestCredentialsVerifier) VerifySystemCredentials(context.Context, coreservice.SystemCredentials, string) error {
	return nil
}

func TestApplicationRuntimeRejectsMissingConfiguredDirectoryBeforeCreatingSession(t *testing.T) {
	runtime, err := openTestApplicationRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	watched := t.TempDir()
	callRuntimeAPI(t, runtime, contracts.MessageTypeDirectoryMonitoringUpdate, coreservice.DirectoryMonitoringResult{Directories: []string{watched}})
	if err := os.Remove(watched); err != nil {
		t.Fatal(err)
	}
	request, err := contracts.NewMessage("missing-directory", contracts.MessageTypeSessionCreate, time.Now().UTC().Add(time.Minute), coreservice.CreateSessionRequest{ID: "missing-session", Name: "目录失效"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtime.api.HandleForClient(request, coreservice.ClientIdentity{UserSID: runtimeTestOwnerSID, WindowsSessionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	var failure coreservice.ErrorResult
	if err := response.DecodePayload(&failure); err != nil {
		t.Fatal(err)
	}
	if response.Type != contracts.MessageTypeError || failure.Code != coreservice.ErrorCodeInvalidPayload {
		t.Fatalf("missing directory create response = %+v", response)
	}
	if _, exists := runtime.coordinator.Current(); exists {
		t.Fatal("invalid directory configuration left a session in progress")
	}
	callRuntimeAPI(t, runtime, contracts.MessageTypeDirectoryMonitoringUpdate, coreservice.DirectoryMonitoringResult{Directories: []string{}})
}
