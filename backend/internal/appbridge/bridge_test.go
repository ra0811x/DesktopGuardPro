package appbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/ipc"
	"desktopguardpro/internal/reporting"
	"desktopguardpro/internal/risk"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
)

func TestBridgeGetsHealth(t *testing.T) {
	t.Parallel()

	api := coreservice.NewAPI(coreservice.NewCoordinator())
	bridge := newBridge(testDialer(api), time.Now)

	health, err := bridge.GetHealth()
	if err != nil {
		t.Fatalf("GetHealth() error = %v", err)
	}
	if health.Status != coreservice.HealthStatusRunning {
		t.Fatalf("health status = %q, want %q", health.Status, coreservice.HealthStatusRunning)
	}
}

func TestBridgeSessionTransitionAllowsBoundedCollectorDrain(t *testing.T) {
	now := time.Now().UTC()
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		if request.DeadlineUTC.Sub(now) < 20*time.Second {
			t.Errorf("collector lifecycle deadline is only %s", request.DeadlineUTC.Sub(now))
		}
		return contracts.MessageTypeSessionResult, coreservice.SessionResult{}
	}), func() time.Time { return now })
	if _, err := bridge.TransitionSession(domain.SessionStateCompleted); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeManagesSession(t *testing.T) {
	t.Parallel()

	api := coreservice.NewAPI(coreservice.NewCoordinator())
	bridge := newBridge(testDialer(api), time.Now)

	created, err := bridge.CreateSession("session-1", "离席保护")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if created.Session == nil || created.Session.State != domain.SessionStateDraft {
		t.Fatalf("created session = %#v, want draft", created.Session)
	}

	transitioned, err := bridge.TransitionSession(domain.SessionStatePreparing)
	if err != nil {
		t.Fatalf("TransitionSession() error = %v", err)
	}
	if transitioned.Session == nil || transitioned.Session.State != domain.SessionStatePreparing {
		t.Fatalf("transitioned session = %#v, want preparing", transitioned.Session)
	}

	current, err := bridge.GetCurrentSession()
	if err != nil {
		t.Fatalf("GetCurrentSession() error = %v", err)
	}
	if current.Session == nil || current.Session.ID != "session-1" {
		t.Fatalf("current session = %#v, want session-1", current.Session)
	}
}

func TestBridgeGetsAndResolvesBaselineReview(t *testing.T) {
	t.Parallel()

	var resolutionRequest coreservice.SessionBaselineReviewResolveRequest
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		switch request.Type {
		case contracts.MessageTypeSessionBaselineReviewGet:
			return contracts.MessageTypeSessionBaselineReviewResult, coreservice.SessionBaselineReviewResult{
				SessionID: "session-1",
				Decision: domain.BaselineStartDecision{
					Status: domain.BaselineCaptureStatusPartialFailure, RequiresUserChoice: true,
					CanContinue: true, CanCancel: true,
					Failures: []domain.BaselineCaptureFailure{{Item: "software", Reason: "access denied"}},
				},
			}
		case contracts.MessageTypeSessionBaselineReviewResolve:
			_ = request.DecodePayload(&resolutionRequest)
			return contracts.MessageTypeSessionBaselineReviewResult, coreservice.SessionBaselineReviewResult{
				SessionID: "session-1", Resolution: storage.BaselineReviewResolutionContinue,
				Session: &domain.Session{ID: "session-1", Name: "review", State: domain.SessionStateActive},
			}
		default:
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
	}), time.Now)

	review, err := bridge.GetSessionBaselineReview("session-1")
	if err != nil || len(review.Decision.Failures) != 1 || !review.Decision.CanContinue {
		t.Fatalf("GetSessionBaselineReview() result=%+v error=%v", review, err)
	}
	resolved, err := bridge.ResolveSessionBaselineReview("session-1", storage.BaselineReviewResolutionContinue)
	if err != nil || resolved.Session == nil || resolved.Session.State != domain.SessionStateActive ||
		resolutionRequest.Resolution != storage.BaselineReviewResolutionContinue {
		t.Fatalf("ResolveSessionBaselineReview() result=%+v request=%+v error=%v", resolved, resolutionRequest, err)
	}
}

func TestBridgeReturnsRemoteError(t *testing.T) {
	t.Parallel()

	api := coreservice.NewAPI(coreservice.NewCoordinator())
	bridge := newBridge(testDialer(api), time.Now)
	if _, err := bridge.CreateSession("session-1", "离席保护"); err != nil {
		t.Fatalf("CreateSession(first) error = %v", err)
	}

	_, err := bridge.CreateSession("session-2", "设备借用")
	var remoteError *RemoteError
	if !errors.As(err, &remoteError) {
		t.Fatalf("CreateSession(second) error = %v, want RemoteError", err)
	}
	if remoteError.Code != coreservice.ErrorCodeSessionInProgress {
		t.Fatalf("remote error code = %q, want %q", remoteError.Code, coreservice.ErrorCodeSessionInProgress)
	}
}

func TestBridgeEndsSessionWithOneTimeSystemCredentialVerification(t *testing.T) {
	t.Parallel()

	var finalizingRequest coreservice.TransitionSessionRequest
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		switch request.Type {
		case contracts.MessageTypeSessionEndVerificationCreate:
			return contracts.MessageTypeSessionEndVerificationResult, coreservice.EndVerificationChallenge{
				Token: "one-time-challenge", ExpiresUTC: time.Now().UTC().Add(time.Minute),
			}
		case contracts.MessageTypeSessionTransition:
			_ = request.DecodePayload(&finalizingRequest)
			return contracts.MessageTypeSessionResult, coreservice.SessionResult{Session: &domain.Session{
				ID: "session-1", Name: "离席保护", State: domain.SessionStateFinalizing,
			}}
		default:
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
	}), time.Now)
	bridge.credentialPrompt = func() (coreservice.SystemCredentials, error) {
		return coreservice.SystemCredentials{UserName: "Raymond", Password: []byte("correct")}, nil
	}

	result, err := bridge.EndSession()
	if err != nil {
		t.Fatalf("EndSession() error = %v", err)
	}
	if result.Session == nil || result.Session.State != domain.SessionStateFinalizing {
		t.Fatalf("EndSession() result = %+v", result)
	}
	if finalizingRequest.State != domain.SessionStateFinalizing || finalizingRequest.Verification == nil ||
		finalizingRequest.Verification.Token != "one-time-challenge" || finalizingRequest.Verification.UserName != "Raymond" {
		t.Fatalf("finalizing request = %+v", finalizingRequest)
	}
}

func TestBridgeAnalysisMethods(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 14, 0, 0, 0, time.UTC)
	var timelineRequest coreservice.TimelineQueryRequest
	var statusRequest coreservice.RiskFindingStatusUpdateRequest
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		switch request.Type {
		case contracts.MessageTypeTimelineQuery:
			_ = request.DecodePayload(&timelineRequest)
			return contracts.MessageTypeTimelineResult, storage.TimelinePage{IntegrityVerified: true}
		case contracts.MessageTypeRiskEvaluate:
			return contracts.MessageTypeRiskResult, risk.Evaluation{SessionID: "session-1"}
		case contracts.MessageTypeRiskFindingStatusUpdate:
			_ = request.DecodePayload(&statusRequest)
			return contracts.MessageTypeRiskFindingStatusResult, coreservice.RiskFindingStatusResult(statusRequest)
		case contracts.MessageTypeReportExport:
			return contracts.MessageTypeReportResult, coreservice.ReportResult{
				Format: coreservice.ReportFormatMarkdown, MediaType: "text/markdown; charset=utf-8",
				Content: "# report", GeneratedUTC: now,
			}
		default:
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
	}), func() time.Time { return now })

	page, err := bridge.QueryTimeline(coreservice.TimelineQueryRequest{SessionID: "session-1", Limit: 25})
	if err != nil || !page.IntegrityVerified || timelineRequest.Limit != 25 {
		t.Fatalf("QueryTimeline() page=%+v request=%+v error=%v", page, timelineRequest, err)
	}
	evaluation, err := bridge.EvaluateRisk("session-1")
	if err != nil || evaluation.SessionID != "session-1" {
		t.Fatalf("EvaluateRisk() result=%+v error=%v", evaluation, err)
	}
	status, err := bridge.UpdateRiskFindingStatus(coreservice.RiskFindingStatusUpdateRequest{
		SessionID: "session-1", FindingID: "finding-1", Status: risk.FindingStatusKnown,
	})
	if err != nil || status.Status != risk.FindingStatusKnown || statusRequest.FindingID != "finding-1" {
		t.Fatalf("UpdateRiskFindingStatus() result=%+v request=%+v error=%v", status, statusRequest, err)
	}
	report, err := bridge.ExportReport(coreservice.ReportExportRequest{
		SessionID: "session-1", Format: coreservice.ReportFormatMarkdown,
	})
	if err != nil || report.Content != "# report" {
		t.Fatalf("ExportReport() result=%+v error=%v", report, err)
	}
}

func TestBridgeQueriesAssetDifferences(t *testing.T) {
	now := time.Date(2026, 8, 23, 14, 0, 0, 0, time.UTC)
	var received coreservice.AssetDifferenceQueryRequest
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		if request.Type != contracts.MessageTypeAssetDifferenceQuery {
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
		_ = request.DecodePayload(&received)
		return contracts.MessageTypeAssetDifferenceResult, coreservice.AssetDifferenceResult{
			SessionID: "session-1", BaselineAvailable: true,
			Differences: []domain.AssetDifference{{
				Kind: domain.AssetDifferenceAdded,
				After: &domain.Asset{
					Category: domain.AssetCategoryDevice, Identifier: `USB\VID_1234`, DisplayName: "Added USB Device",
				},
			}},
		}
	}), func() time.Time { return now })

	result, err := bridge.QueryAssetDifferences(coreservice.AssetDifferenceQueryRequest{
		SessionID: "session-1", Categories: []domain.AssetCategory{domain.AssetCategoryDevice},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.BaselineAvailable || len(result.Differences) != 1 ||
		received.SessionID != "session-1" || len(received.Categories) != 1 ||
		received.Categories[0] != domain.AssetCategoryDevice {
		t.Fatalf("query result=%+v request=%+v", result, received)
	}
}

func TestBridgeManagesSessionRetention(t *testing.T) {
	now := time.Date(2026, time.September, 7, 5, 0, 0, 0, time.UTC)
	var lockRequest coreservice.SessionRetentionLockRequest
	var pruneRequest coreservice.SessionRetentionPruneRequest
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		switch request.Type {
		case contracts.MessageTypeSessionRetentionLockUpdate:
			_ = request.DecodePayload(&lockRequest)
			return contracts.MessageTypeSessionRetentionLockResult, coreservice.SessionRetentionLockResult{
				SessionID: lockRequest.SessionID, Locked: lockRequest.Locked,
			}
		case contracts.MessageTypeSessionRetentionPrune:
			_ = request.DecodePayload(&pruneRequest)
			return contracts.MessageTypeSessionRetentionPruneResult, coreservice.SessionRetentionPruneResult{
				DeletedSessionIDs: []string{"expired-session"},
			}
		default:
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
	}), func() time.Time { return now })
	lock, err := bridge.UpdateSessionRetentionLock(coreservice.SessionRetentionLockRequest{SessionID: "session-1", Locked: true})
	if err != nil || !lock.Locked || lock.SessionID != "session-1" || lockRequest.SessionID != "session-1" {
		t.Fatalf("lock result=%+v request=%+v error=%v", lock, lockRequest, err)
	}
	before := now.Add(-30 * 24 * time.Hour)
	pruned, err := bridge.PruneSessionRetention(coreservice.SessionRetentionPruneRequest{BeforeUTC: before, Limit: 20})
	if err != nil || len(pruned.DeletedSessionIDs) != 1 || pruneRequest.BeforeUTC != before || pruneRequest.Limit != 20 {
		t.Fatalf("prune result=%+v request=%+v error=%v", pruned, pruneRequest, err)
	}
}

func TestBridgeRejectsMismatchedRiskSession(t *testing.T) {
	t.Parallel()

	bridge := newBridge(testMessageDialer(func(contracts.Message) (contracts.MessageType, any) {
		return contracts.MessageTypeRiskResult, risk.Evaluation{SessionID: "another-session"}
	}), time.Now)
	if _, err := bridge.EvaluateRisk("session-1"); err == nil {
		t.Fatal("EvaluateRisk() accepted a mismatched session")
	}
}

func TestBridgeReassemblesAndVerifiesReportTransfer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 15, 30, 0, 0, time.UTC)
	content := []byte("# transferred report")
	digest := sha256.Sum256(content)
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		switch request.Type {
		case contracts.MessageTypeReportExport:
			return contracts.MessageTypeReportResult, coreservice.ReportResult{
				Format: coreservice.ReportFormatMarkdown, MediaType: "text/markdown; charset=utf-8",
				GeneratedUTC: now,
				Transfer: &coreservice.ReportTransfer{
					Token: token, Size: len(content), SHA256: hex.EncodeToString(digest[:]),
					ChunkBytes: 7, ExpiresUTC: now.Add(time.Minute),
				},
			}
		case contracts.MessageTypeReportChunkGet:
			var payload coreservice.ReportChunkRequest
			_ = request.DecodePayload(&payload)
			end := min(payload.Offset+7, len(content))
			return contracts.MessageTypeReportChunkResult, coreservice.ReportChunkResult{
				Content: content[payload.Offset:end], NextOffset: end, Done: end == len(content),
			}
		default:
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
	}), func() time.Time { return now })

	result, err := bridge.ExportReport(coreservice.ReportExportRequest{
		SessionID: "session-1", Format: coreservice.ReportFormatMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != string(content) || result.Transfer != nil {
		t.Fatalf("unexpected assembled report: %+v", result)
	}
}

func TestBridgeAcceptsJSONReportWithVerificationManifest(t *testing.T) {
	now := time.Date(2026, time.September, 7, 11, 0, 0, 0, time.UTC)
	content := `{"schemaVersion":1}`
	digest := sha256.Sum256([]byte(content))
	result := coreservice.ReportResult{
		Format: coreservice.ReportFormatJSON, MediaType: "application/json", Content: content,
		GeneratedUTC: now,
		Verification: &reporting.VerificationManifest{
			SchemaVersion: 1, Algorithm: "SHA-256", ReportSHA256: hex.EncodeToString(digest[:]),
			ReportSize: len(content), MediaType: "application/json", SessionID: "session-1",
			ReportGeneratedUTC: now, IntegrityVerified: true,
		},
	}
	if err := validateReportResult(coreservice.ReportExportRequest{
		SessionID: "session-1", Format: coreservice.ReportFormatJSON,
	}, result); err != nil {
		t.Fatalf("validateReportResult() error = %v", err)
	}
}

func TestBridgeRejectsMismatchedReportVerificationManifest(t *testing.T) {
	now := time.Date(2026, time.September, 7, 11, 5, 0, 0, time.UTC)
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		if request.Type != contracts.MessageTypeReportExport {
			return contracts.MessageTypeError, coreservice.ErrorResult{Code: "unexpected", Message: "unexpected request"}
		}
		return contracts.MessageTypeReportResult, coreservice.ReportResult{
			Format: coreservice.ReportFormatMarkdown, MediaType: "text/markdown; charset=utf-8", Content: "# report",
			GeneratedUTC: now,
			Verification: &reporting.VerificationManifest{
				SchemaVersion: 1, Algorithm: "SHA-256", ReportSHA256: strings.Repeat("0", 64),
				ReportSize: len("# report"), MediaType: "text/markdown; charset=utf-8", SessionID: "session-1",
				ReportGeneratedUTC: now, IntegrityVerified: true,
			},
		}
	}), func() time.Time { return now })
	if _, err := bridge.ExportReport(coreservice.ReportExportRequest{
		SessionID: "session-1", Format: coreservice.ReportFormatMarkdown,
	}); err == nil {
		t.Fatal("ExportReport() accepted a mismatched verification manifest")
	}
}

func TestBridgeRejectsReportDigestMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 15, 30, 0, 0, time.UTC)
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		if request.Type == contracts.MessageTypeReportExport {
			return contracts.MessageTypeReportResult, coreservice.ReportResult{
				Format: coreservice.ReportFormatMarkdown, MediaType: "text/markdown; charset=utf-8", GeneratedUTC: now,
				Transfer: &coreservice.ReportTransfer{
					Token: token, Size: 3, SHA256: strings.Repeat("0", 64), ChunkBytes: 3, ExpiresUTC: now.Add(time.Minute),
				},
			}
		}
		return contracts.MessageTypeReportChunkResult, coreservice.ReportChunkResult{Content: []byte("bad"), NextOffset: 3, Done: true}
	}), func() time.Time { return now })
	if _, err := bridge.ExportReport(coreservice.ReportExportRequest{
		SessionID: "session-1", Format: coreservice.ReportFormatMarkdown,
	}); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
}

func testDialer(api *coreservice.API) dialFunc {
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			request, err := ipc.ReadMessage(server)
			if err != nil {
				return
			}
			response, err := api.Handle(request)
			if err != nil {
				return
			}
			_ = ipc.WriteMessage(server, response)
		}()
		return client, nil
	}
}

func testMessageDialer(respond func(contracts.Message) (contracts.MessageType, any)) dialFunc {
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			request, err := ipc.ReadMessage(server)
			if err != nil {
				return
			}
			responseType, payload := respond(request)
			response, err := contracts.NewMessage(request.RequestID, responseType, request.DeadlineUTC, payload)
			if err != nil {
				return
			}
			_ = ipc.WriteMessage(server, response)
		}()
		return client, nil
	}
}
