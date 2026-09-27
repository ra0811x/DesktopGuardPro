package appbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/ipc"
	"desktopguardpro/internal/reporting"
	"desktopguardpro/internal/risk"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
	platformwindows "desktopguardpro/internal/windows"
)

const (
	requestTimeout         = 3 * time.Second
	sessionRequestTimeout  = 30 * time.Second
	analysisRequestTimeout = 15 * time.Second
	reportRequestTimeout   = 30 * time.Second
)

type dialFunc func(context.Context) (net.Conn, error)
type credentialPromptFunc func() (coreservice.SystemCredentials, error)

type Bridge struct {
	dial             dialFunc
	now              func() time.Time
	credentialPrompt credentialPromptFunc
}

type RemoteError struct {
	Code    string
	Message string
}

func (remoteError *RemoteError) Error() string {
	return fmt.Sprintf("service error %s: %s", remoteError.Code, remoteError.Message)
}

func New() *Bridge {
	return newBridge(func(ctx context.Context) (net.Conn, error) {
		return platformwindows.DialPipe(ctx, platformwindows.ControlPipePath)
	}, time.Now)
}

func newBridge(dial dialFunc, now func() time.Time) *Bridge {
	return &Bridge{
		dial: dial, now: now, credentialPrompt: platformwindows.PromptForSystemCredentials,
	}
}

func (bridge *Bridge) GetHealth() (coreservice.HealthResult, error) {
	var result coreservice.HealthResult
	err := bridge.call(
		contracts.MessageTypeHealthGet,
		struct{}{},
		contracts.MessageTypeHealthResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) CreateSession(id, name string) (coreservice.SessionResult, error) {
	return bridge.CreateSessionWithMonitoringLevel(id, name, domain.MonitoringLevelStandard)
}

func (bridge *Bridge) CreateSessionWithMonitoringLevel(id, name string, level domain.MonitoringLevel) (coreservice.SessionResult, error) {
	var result coreservice.SessionResult
	err := bridge.call(
		contracts.MessageTypeSessionCreate,
		coreservice.CreateSessionRequest{ID: id, Name: name, MonitoringLevel: level},
		contracts.MessageTypeSessionResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) GetSessionStartPreview(level domain.MonitoringLevel) (coreservice.SessionStartPreviewResult, error) {
	var result coreservice.SessionStartPreviewResult
	err := bridge.call(
		contracts.MessageTypeSessionStartPreviewGet,
		coreservice.SessionStartPreviewRequest{MonitoringLevel: level},
		contracts.MessageTypeSessionStartPreviewResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) TransitionSession(
	state domain.SessionState,
) (coreservice.SessionResult, error) {
	var result coreservice.SessionResult
	err := bridge.callWithTimeout(
		contracts.MessageTypeSessionTransition,
		coreservice.TransitionSessionRequest{State: state},
		contracts.MessageTypeSessionResult,
		&result,
		sessionRequestTimeout,
	)
	return result, err
}

func (bridge *Bridge) EndSession() (coreservice.SessionResult, error) {
	var challenge coreservice.EndVerificationChallenge
	if err := bridge.call(
		contracts.MessageTypeSessionEndVerificationCreate,
		struct{}{},
		contracts.MessageTypeSessionEndVerificationResult,
		&challenge,
	); err != nil {
		return coreservice.SessionResult{}, err
	}
	credentials, err := bridge.credentialPrompt()
	if err != nil {
		return coreservice.SessionResult{}, fmt.Errorf("request Windows system credentials: %w", err)
	}
	defer clear(credentials.Password)

	var result coreservice.SessionResult
	err = bridge.callWithTimeout(
		contracts.MessageTypeSessionTransition,
		coreservice.TransitionSessionRequest{
			State: domain.SessionStateFinalizing,
			Verification: &coreservice.EndProtectionVerification{
				Token:    challenge.Token,
				UserName: credentials.UserName,
				Domain:   credentials.Domain,
				Password: credentials.Password,
			},
		},
		contracts.MessageTypeSessionResult,
		&result,
		sessionRequestTimeout,
	)
	return result, err
}

func (bridge *Bridge) GetCurrentSession() (coreservice.SessionResult, error) {
	var result coreservice.SessionResult
	err := bridge.call(
		contracts.MessageTypeSessionCurrentGet,
		struct{}{},
		contracts.MessageTypeSessionResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) GetSessionBaselineReview(sessionID string) (coreservice.SessionBaselineReviewResult, error) {
	var result coreservice.SessionBaselineReviewResult
	normalizedSessionID := strings.TrimSpace(sessionID)
	err := bridge.call(
		contracts.MessageTypeSessionBaselineReviewGet,
		coreservice.SessionBaselineReviewGetRequest{SessionID: normalizedSessionID},
		contracts.MessageTypeSessionBaselineReviewResult,
		&result,
	)
	if err == nil && result.SessionID != normalizedSessionID {
		err = errors.New("service returned an invalid baseline review")
	}
	return result, err
}

func (bridge *Bridge) ResolveSessionBaselineReview(
	sessionID string,
	resolution storage.BaselineReviewResolution,
) (coreservice.SessionBaselineReviewResult, error) {
	var result coreservice.SessionBaselineReviewResult
	normalizedSessionID := strings.TrimSpace(sessionID)
	err := bridge.callWithTimeout(
		contracts.MessageTypeSessionBaselineReviewResolve,
		coreservice.SessionBaselineReviewResolveRequest{SessionID: normalizedSessionID, Resolution: resolution},
		contracts.MessageTypeSessionBaselineReviewResult,
		&result,
		sessionRequestTimeout,
	)
	if err == nil && (result.SessionID != normalizedSessionID || result.Resolution != resolution || result.Session == nil) {
		err = errors.New("service returned an invalid baseline review resolution")
	}
	return result, err
}

func (bridge *Bridge) UpdateSessionRetentionLock(
	request coreservice.SessionRetentionLockRequest,
) (coreservice.SessionRetentionLockResult, error) {
	var result coreservice.SessionRetentionLockResult
	err := bridge.call(
		contracts.MessageTypeSessionRetentionLockUpdate,
		request,
		contracts.MessageTypeSessionRetentionLockResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) PruneSessionRetention(
	request coreservice.SessionRetentionPruneRequest,
) (coreservice.SessionRetentionPruneResult, error) {
	var result coreservice.SessionRetentionPruneResult
	err := bridge.call(
		contracts.MessageTypeSessionRetentionPrune,
		request,
		contracts.MessageTypeSessionRetentionPruneResult,
		&result,
	)
	return result, err
}

func (bridge *Bridge) QueryTimeline(request coreservice.TimelineQueryRequest) (storage.TimelinePage, error) {
	var result storage.TimelinePage
	err := bridge.callWithTimeout(
		contracts.MessageTypeTimelineQuery,
		request,
		contracts.MessageTypeTimelineResult,
		&result,
		analysisRequestTimeout,
	)
	if err == nil {
		err = validateTimelineResult(request, result)
	}
	return result, err
}

func (bridge *Bridge) EvaluateRisk(sessionID string) (risk.Evaluation, error) {
	var result risk.Evaluation
	err := bridge.callWithTimeout(
		contracts.MessageTypeRiskEvaluate,
		coreservice.RiskEvaluateRequest{SessionID: sessionID},
		contracts.MessageTypeRiskResult,
		&result,
		analysisRequestTimeout,
	)
	if err == nil {
		err = validateRiskResult(strings.TrimSpace(sessionID), result)
	}
	return result, err
}

func (bridge *Bridge) UpdateRiskFindingStatus(
	request coreservice.RiskFindingStatusUpdateRequest,
) (coreservice.RiskFindingStatusResult, error) {
	var result coreservice.RiskFindingStatusResult
	err := bridge.callWithTimeout(
		contracts.MessageTypeRiskFindingStatusUpdate,
		request,
		contracts.MessageTypeRiskFindingStatusResult,
		&result,
		analysisRequestTimeout,
	)
	if err == nil && (result.SessionID != strings.TrimSpace(request.SessionID) ||
		result.FindingID != strings.TrimSpace(request.FindingID) || result.Status != request.Status) {
		err = errors.New("service returned an invalid risk finding status")
	}
	return result, err
}

func (bridge *Bridge) QueryAssetDifferences(
	request coreservice.AssetDifferenceQueryRequest,
) (coreservice.AssetDifferenceResult, error) {
	var result coreservice.AssetDifferenceResult
	err := bridge.callWithTimeout(
		contracts.MessageTypeAssetDifferenceQuery,
		request,
		contracts.MessageTypeAssetDifferenceResult,
		&result,
		analysisRequestTimeout,
	)
	if err == nil {
		err = validateAssetDifferenceResult(request, result)
	}
	return result, err
}

func (bridge *Bridge) ExportReport(request coreservice.ReportExportRequest) (coreservice.ReportResult, error) {
	var result coreservice.ReportResult
	err := bridge.callWithTimeout(
		contracts.MessageTypeReportExport,
		request,
		contracts.MessageTypeReportResult,
		&result,
		reportRequestTimeout,
	)
	if err == nil {
		err = validateReportResult(request, result)
	}
	if err == nil && result.Transfer != nil {
		result.Content, err = bridge.readReportTransfer(*result.Transfer)
		if err == nil {
			result.Transfer = nil
		}
	}
	if err == nil && result.Verification != nil {
		err = reporting.VerifyReportContent(*result.Verification, []byte(result.Content))
	}
	return result, err
}

func (bridge *Bridge) call(
	messageType contracts.MessageType,
	payload any,
	wantType contracts.MessageType,
	target any,
) error {
	return bridge.callWithTimeout(messageType, payload, wantType, target, requestTimeout)
}

func (bridge *Bridge) callWithTimeout(
	messageType contracts.MessageType,
	payload any,
	wantType contracts.MessageType,
	target any,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	requestID, err := newRequestID()
	if err != nil {
		return fmt.Errorf("create request id: %w", err)
	}
	messageDeadline := bridge.now().UTC().Add(timeout)
	request, err := contracts.NewMessage(requestID, messageType, messageDeadline, payload)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	connection, err := bridge.dial(ctx)
	if err != nil {
		return fmt.Errorf("connect to service: %w", err)
	}
	defer connection.Close()
	transportDeadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("service request context has no deadline")
	}
	if err := connection.SetDeadline(transportDeadline); err != nil {
		return fmt.Errorf("set service deadline: %w", err)
	}

	if err := ipc.WriteMessage(connection, request); err != nil {
		return fmt.Errorf("write service request: %w", err)
	}
	response, err := ipc.ReadMessage(connection)
	if err != nil {
		return fmt.Errorf("read service response: %w", err)
	}
	if response.RequestID != requestID {
		return fmt.Errorf("service response request id mismatch")
	}
	if response.Type == contracts.MessageTypeError {
		var result coreservice.ErrorResult
		if err := response.DecodePayload(&result); err != nil {
			return fmt.Errorf("decode service error: %w", err)
		}
		return &RemoteError{Code: result.Code, Message: result.Message}
	}
	if response.Type != wantType {
		return fmt.Errorf("unexpected service response type %q", response.Type)
	}
	if err := response.DecodePayload(target); err != nil {
		return fmt.Errorf("decode service response: %w", err)
	}
	return nil
}

func validateTimelineResult(request coreservice.TimelineQueryRequest, result storage.TimelinePage) error {
	if result.HasMore != (strings.TrimSpace(result.NextCursor) != "") ||
		(request.Limit > 0 && len(result.Records) > request.Limit) {
		return errors.New("service returned an invalid timeline page")
	}
	var previousSequence uint64
	for index, record := range result.Records {
		if err := record.Event.Validate(); err != nil || record.Event.SessionID != strings.TrimSpace(request.SessionID) ||
			(index > 0 && record.Event.Sequence <= previousSequence) {
			return errors.New("service returned an invalid timeline event")
		}
		previousSequence = record.Event.Sequence
	}
	return nil
}

func validateRiskResult(sessionID string, result risk.Evaluation) error {
	if sessionID == "" || result.SessionID != sessionID {
		return errors.New("service returned an invalid risk evaluation")
	}
	for _, finding := range result.Findings {
		if err := finding.Validate(); err != nil || finding.SessionID != sessionID {
			return errors.New("service returned an invalid risk finding")
		}
	}
	for _, failure := range result.Failures {
		if strings.TrimSpace(failure.RuleID) == "" || strings.TrimSpace(failure.Message) == "" {
			return errors.New("service returned an invalid risk rule failure")
		}
	}
	return nil
}

func validateAssetDifferenceResult(
	request coreservice.AssetDifferenceQueryRequest,
	result coreservice.AssetDifferenceResult,
) error {
	if strings.TrimSpace(request.SessionID) == "" || result.SessionID != strings.TrimSpace(request.SessionID) ||
		(!result.BaselineAvailable && len(result.Differences) != 0) {
		return errors.New("service returned an invalid asset difference result")
	}
	allowed := make(map[domain.AssetCategory]struct{}, len(request.Categories))
	for _, category := range request.Categories {
		allowed[category] = struct{}{}
	}
	for _, difference := range result.Differences {
		asset, err := validateAssetDifference(difference)
		if err != nil {
			return err
		}
		if len(allowed) > 0 {
			if _, exists := allowed[asset.Category]; !exists {
				return errors.New("service returned an asset outside the requested categories")
			}
		}
	}
	return nil
}

func validateAssetDifference(difference domain.AssetDifference) (*domain.Asset, error) {
	switch difference.Kind {
	case domain.AssetDifferenceAdded:
		if difference.Before != nil || difference.After == nil || len(difference.ChangedAttributes) != 0 {
			return nil, errors.New("service returned an invalid added asset difference")
		}
		if err := difference.After.Validate(); err != nil {
			return nil, errors.New("service returned an invalid added asset")
		}
		return difference.After, nil
	case domain.AssetDifferenceRemoved:
		if difference.Before == nil || difference.After != nil || len(difference.ChangedAttributes) != 0 {
			return nil, errors.New("service returned an invalid removed asset difference")
		}
		if err := difference.Before.Validate(); err != nil {
			return nil, errors.New("service returned an invalid removed asset")
		}
		return difference.Before, nil
	case domain.AssetDifferenceChanged:
		if difference.Before == nil || difference.After == nil || len(difference.ChangedAttributes) == 0 ||
			difference.Before.Category != difference.After.Category || difference.Before.Identifier != difference.After.Identifier {
			return nil, errors.New("service returned an invalid changed asset difference")
		}
		if err := difference.Before.Validate(); err != nil {
			return nil, errors.New("service returned an invalid prior asset")
		}
		if err := difference.After.Validate(); err != nil {
			return nil, errors.New("service returned an invalid current asset")
		}
		for _, attribute := range difference.ChangedAttributes {
			if strings.TrimSpace(attribute) == "" {
				return nil, errors.New("service returned an invalid changed asset field")
			}
		}
		return difference.After, nil
	default:
		return nil, errors.New("service returned an unknown asset difference kind")
	}
}

func validateReportResult(request coreservice.ReportExportRequest, result coreservice.ReportResult) error {
	wantMediaType := "text/html; charset=utf-8"
	switch request.Format {
	case coreservice.ReportFormatMarkdown:
		wantMediaType = "text/markdown; charset=utf-8"
	case coreservice.ReportFormatJSON:
		wantMediaType = "application/json"
	}
	hasContent := result.Content != ""
	hasTransfer := result.Transfer != nil
	if result.Format != request.Format || result.MediaType != wantMediaType || hasContent == hasTransfer || result.GeneratedUTC.IsZero() {
		return errors.New("service returned an invalid report result")
	}
	_, offset := result.GeneratedUTC.Zone()
	if offset != 0 {
		return errors.New("service returned a report timestamp outside UTC")
	}
	if verification := result.Verification; verification != nil &&
		(verification.MediaType != result.MediaType || verification.SessionID != request.SessionID ||
			!verification.ReportGeneratedUTC.Equal(result.GeneratedUTC)) {
		return errors.New("service returned an invalid report verification manifest")
	}
	if result.Transfer != nil {
		transfer := result.Transfer
		if len(strings.TrimSpace(transfer.Token)) < 32 || transfer.Size < 1 || transfer.ChunkBytes < 1 ||
			len(transfer.SHA256) != sha256.Size*2 || transfer.ExpiresUTC.IsZero() {
			return errors.New("service returned an invalid report transfer")
		}
		if _, err := hex.DecodeString(transfer.SHA256); err != nil {
			return errors.New("service returned an invalid report digest")
		}
		_, transferOffset := transfer.ExpiresUTC.Zone()
		if transferOffset != 0 {
			return errors.New("service returned a report expiry outside UTC")
		}
	}
	return nil
}

func (bridge *Bridge) readReportTransfer(transfer coreservice.ReportTransfer) (string, error) {
	var content bytes.Buffer
	content.Grow(transfer.Size)
	offset := 0
	maximumChunks := transfer.Size/transfer.ChunkBytes + 2
	for chunkIndex := 0; chunkIndex < maximumChunks; chunkIndex++ {
		var chunk coreservice.ReportChunkResult
		err := bridge.callWithTimeout(
			contracts.MessageTypeReportChunkGet,
			coreservice.ReportChunkRequest{Token: transfer.Token, Offset: offset},
			contracts.MessageTypeReportChunkResult,
			&chunk,
			reportRequestTimeout,
		)
		if err != nil {
			return "", err
		}
		if len(chunk.Content) == 0 || chunk.NextOffset != offset+len(chunk.Content) ||
			chunk.NextOffset > transfer.Size || chunk.Done != (chunk.NextOffset == transfer.Size) {
			return "", errors.New("service returned an invalid report chunk")
		}
		_, _ = content.Write(chunk.Content)
		offset = chunk.NextOffset
		if chunk.Done {
			if content.Len() != transfer.Size || !utf8.Valid(content.Bytes()) {
				return "", errors.New("service returned invalid report content")
			}
			digest := sha256.Sum256(content.Bytes())
			if !strings.EqualFold(hex.EncodeToString(digest[:]), transfer.SHA256) {
				return "", errors.New("service returned a report digest mismatch")
			}
			return content.String(), nil
		}
	}
	return "", errors.New("service report transfer did not complete")
}

func newRequestID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(randomBytes), nil
}
