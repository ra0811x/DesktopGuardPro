package appbridge

import (
	"reflect"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
)

func TestBridgeDirectoryMonitoringUsesServiceConfiguration(t *testing.T) {
	stored := []string{`C:\Documents`}
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		if request.Type == contracts.MessageTypeDirectoryMonitoringUpdate {
			var settings coreservice.DirectoryMonitoringResult
			_ = request.DecodePayload(&settings)
			stored = settings.Directories
		} else if request.Type != contracts.MessageTypeDirectoryMonitoringGet {
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
		return contracts.MessageTypeDirectoryMonitoringResult, coreservice.DirectoryMonitoringResult{Directories: stored}
	}), time.Now)
	loaded, err := bridge.GetDirectoryMonitoring()
	if err != nil || !reflect.DeepEqual(loaded.Directories, stored) {
		t.Fatalf("GetDirectoryMonitoring() = %+v, %v", loaded, err)
	}
	want := []string{`D:\重点目录`}
	updated, err := bridge.UpdateDirectoryMonitoring(want)
	if err != nil || !reflect.DeepEqual(updated.Directories, want) || !reflect.DeepEqual(stored, want) {
		t.Fatalf("UpdateDirectoryMonitoring() = %+v, stored = %v, error = %v", updated, stored, err)
	}
}

func TestBridgeUpdatesTypedMonitoringTargets(t *testing.T) {
	var stored []domain.MonitoringTarget
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		if request.Type != contracts.MessageTypeDirectoryMonitoringUpdate {
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
		var settings coreservice.DirectoryMonitoringResult
		if err := request.DecodePayload(&settings); err != nil {
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "decode", Message: err.Error()}
		}
		stored = settings.Targets
		return contracts.MessageTypeDirectoryMonitoringResult, coreservice.DirectoryMonitoringResult{Targets: stored}
	}), time.Now)
	want := []domain.MonitoringTarget{
		{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: false},
		{Path: `C:\Evidence\focus.txt`, Kind: domain.MonitoringTargetKindFile},
		{Path: `E:\`, Kind: domain.MonitoringTargetKindRemovableVolume},
	}
	updated, err := bridge.UpdateMonitoringTargets(want)
	if err != nil || !reflect.DeepEqual(updated.Targets, want) || !reflect.DeepEqual(stored, want) {
		t.Fatalf("UpdateMonitoringTargets() = %+v, stored = %#v, error = %v", updated, stored, err)
	}
}

func TestBridgeRequestsStartPreviewAndCreatesSelectedMonitoringLevel(t *testing.T) {
	requests := make([]contracts.MessageType, 0, 2)
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		requests = append(requests, request.Type)
		switch request.Type {
		case contracts.MessageTypeSessionStartPreviewGet:
			var payload coreservice.SessionStartPreviewRequest
			_ = request.DecodePayload(&payload)
			return contracts.MessageTypeSessionStartPreviewResult, coreservice.SessionStartPreviewResult{
				MonitoringLevel: payload.MonitoringLevel, MonitoredTargetCount: 3,
			}
		case contracts.MessageTypeSessionCreate:
			var payload coreservice.CreateSessionRequest
			_ = request.DecodePayload(&payload)
			return contracts.MessageTypeSessionResult, coreservice.SessionResult{Session: &domain.Session{
				ID: payload.ID, Name: payload.Name, State: domain.SessionStateDraft, MonitoringLevel: payload.MonitoringLevel,
			}}
		default:
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
	}), time.Now)
	preview, err := bridge.GetSessionStartPreview(domain.MonitoringLevelStrict)
	if err != nil || preview.MonitoringLevel != domain.MonitoringLevelStrict || preview.MonitoredTargetCount != 3 {
		t.Fatalf("GetSessionStartPreview() = %+v, %v", preview, err)
	}
	created, err := bridge.CreateSessionWithMonitoringLevel("strict-1", "严格保护", domain.MonitoringLevelStrict)
	if err != nil || created.Session == nil || created.Session.MonitoringLevel != domain.MonitoringLevelStrict {
		t.Fatalf("CreateSessionWithMonitoringLevel() = %+v, %v", created, err)
	}
	if !reflect.DeepEqual(requests, []contracts.MessageType{contracts.MessageTypeSessionStartPreviewGet, contracts.MessageTypeSessionCreate}) {
		t.Fatalf("requests = %v", requests)
	}
}
