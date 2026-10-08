package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/reporting"
	"desktopguardpro/internal/risk"
	"desktopguardpro/internal/storage"
)

const maximumInlineReportBytes = 768 * 1024

type AnalysisStore interface {
	GetSession(ctx context.Context, id string) (domain.Session, error)
	ListEvents(ctx context.Context, sessionID string) ([]storage.EventRecord, error)
	QueryTimeline(ctx context.Context, query storage.TimelineQuery) (storage.TimelinePage, error)
}

type assetBaselineStore interface {
	LoadAssetBaseline(
		ctx context.Context,
		sessionID string,
		stage storage.AssetBaselineStage,
	) (domain.AssetBaseline, error)
}

type analysisRuntime struct {
	store     AnalysisStore
	evaluator *risk.Evaluator
	exports   *exportStore
	slots     chan struct{}
}

func newAnalysisRuntime(store SessionStore) *analysisRuntime {
	analysisStore, ok := store.(AnalysisStore)
	if !ok {
		return nil
	}
	evaluator, err := risk.NewEvaluator(risk.DefaultRules()...)
	if err != nil {
		return nil
	}
	exports, err := newExportStore(exportStoreOptions{})
	if err != nil {
		return nil
	}
	return &analysisRuntime{store: analysisStore, evaluator: evaluator, exports: exports, slots: make(chan struct{}, 2)}
}

type TimelineQueryRequest struct {
	SessionID   string                 `json:"sessionId"`
	Cursor      string                 `json:"cursor,omitempty"`
	Limit       int                    `json:"limit,omitempty"`
	Categories  []domain.EventCategory `json:"categories,omitempty"`
	Severities  []domain.EventSeverity `json:"severities,omitempty"`
	UserSIDHash string                 `json:"userSidHash,omitempty"`
	User        string                 `json:"user,omitempty"`
	Process     string                 `json:"process,omitempty"`
	Path        string                 `json:"path,omitempty"`
	FromUTC     *time.Time             `json:"fromUtc,omitempty"`
	ToUTC       *time.Time             `json:"toUtc,omitempty"`
}

type RiskEvaluateRequest struct {
	SessionID string `json:"sessionId"`
}

type RiskFindingStatusUpdateRequest struct {
	SessionID string             `json:"sessionId"`
	FindingID string             `json:"findingId"`
	Status    risk.FindingStatus `json:"status"`
}

type RiskFindingStatusResult struct {
	SessionID string             `json:"sessionId"`
	FindingID string             `json:"findingId"`
	Status    risk.FindingStatus `json:"status"`
}

type riskFindingStatusEventStore interface {
	AppendEventAutoSequence(context.Context, domain.AuditEvent, []byte) (domain.AuditEvent, error)
}

type AssetDifferenceQueryRequest struct {
	SessionID  string                 `json:"sessionId"`
	Categories []domain.AssetCategory `json:"categories,omitempty"`
}

type AssetDifferenceResult struct {
	SessionID         string                   `json:"sessionId"`
	BaselineAvailable bool                     `json:"baselineAvailable"`
	Differences       []domain.AssetDifference `json:"differences"`
}

type ReportFormat string

const (
	ReportFormatHTML     ReportFormat = "html"
	ReportFormatMarkdown ReportFormat = "markdown"
	ReportFormatJSON     ReportFormat = "json"
)

type ReportExportRequest struct {
	FromSequence uint64                    `json:"fromSequence,omitempty"`
	ToSequence   uint64                    `json:"toSequence,omitempty"`
	SessionID    string                    `json:"sessionId"`
	Format       ReportFormat              `json:"format"`
	Redaction    reporting.RedactionPolicy `json:"redaction"`
}

type ReportResult struct {
	Format       ReportFormat                    `json:"format"`
	MediaType    string                          `json:"mediaType"`
	Content      string                          `json:"content,omitempty"`
	Transfer     *ReportTransfer                 `json:"transfer,omitempty"`
	Verification *reporting.VerificationManifest `json:"verification,omitempty"`
	GeneratedUTC time.Time                       `json:"generatedUtc"`
}

type ReportTransfer struct {
	Token      string    `json:"token"`
	Size       int       `json:"size"`
	SHA256     string    `json:"sha256"`
	ChunkBytes int       `json:"chunkBytes"`
	ExpiresUTC time.Time `json:"expiresUtc"`
}

type ReportChunkRequest struct {
	Token  string `json:"token"`
	Offset int    `json:"offset"`
}

type ReportChunkResult struct {
	Content    []byte `json:"content"`
	NextOffset int    `json:"nextOffset"`
	Done       bool   `json:"done"`
}

func (api *API) queryTimeline(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	if api.analysis == nil {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "timeline analysis is unavailable")
	}
	var payload TimelineQueryRequest
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "timeline query payload is invalid")
	}
	release, err := api.analysis.acquire(ctx)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	defer release()
	page, err := api.analysis.store.QueryTimeline(ctx, storage.TimelineQuery{
		SessionID: payload.SessionID, Cursor: payload.Cursor, Limit: payload.Limit,
		Categories: payload.Categories, Severities: payload.Severities, UserSIDHash: payload.UserSIDHash,
		User: payload.User, Process: payload.Process, Path: payload.Path, FromUTC: payload.FromUTC, ToUTC: payload.ToUTC,
	})
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeTimelineResult, page)
}

func (api *API) evaluateRisk(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	if api.analysis == nil {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "risk analysis is unavailable")
	}
	var payload RiskEvaluateRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "risk evaluation payload is invalid")
	}
	release, err := api.analysis.acquire(ctx)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	defer release()
	evaluation, _, err := api.evaluateStoredSession(ctx, payload.SessionID)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeRiskResult, evaluation)
}

func (api *API) updateRiskFindingStatus(ctx context.Context, request contracts.Message, client ClientIdentity) (contracts.Message, error) {
	if api.analysis == nil {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "risk analysis is unavailable")
	}
	var payload RiskFindingStatusUpdateRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" ||
		strings.TrimSpace(payload.FindingID) == "" || !validFindingStatus(payload.Status) {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "risk finding status payload is invalid")
	}
	store, ok := api.analysis.store.(riskFindingStatusEventStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "risk finding status storage is unavailable")
	}
	evaluation, _, err := api.evaluateStoredSession(ctx, payload.SessionID)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	previous := risk.FindingStatusPendingReview
	found := false
	for _, finding := range evaluation.Findings {
		if finding.ID == payload.FindingID {
			previous, found = finding.Status, true
			break
		}
	}
	if !found {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "risk finding was not found")
	}
	observedUTC := api.now().UTC()
	eventPayload, err := json.Marshal(struct {
		FindingID string             `json:"findingId"`
		Previous  risk.FindingStatus `json:"previous"`
		Status    risk.FindingStatus `json:"status"`
	}{payload.FindingID, previous, payload.Status})
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	eventID, err := newSessionLifecycleEventID()
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	var userSIDHash []byte
	if strings.TrimSpace(client.UserSID) != "" {
		digest := sha256.Sum256([]byte(strings.TrimSpace(client.UserSID)))
		userSIDHash = digest[:]
	}
	var windowsSessionID *uint32
	if client.WindowsSessionID != 0 {
		value := client.WindowsSessionID
		windowsSessionID = &value
	}
	if _, err := store.AppendEventAutoSequence(ctx, domain.AuditEvent{
		EventID: eventID, SessionID: payload.SessionID, Category: domain.EventCategorySystem,
		Action: "risk_finding_status_changed", Severity: domain.EventSeverityLow,
		ObservedUTC: observedUTC, MonotonicTicks: observedUTC.UnixNano(), ObjectKey: payload.FindingID,
		WindowsSessionID: windowsSessionID, UserSIDHash: userSIDHash,
		Source: "desktop_guard_service", Confidence: domain.EventConfidenceDirect,
	}, eventPayload); err != nil {
		return api.analysisErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeRiskFindingStatusResult, RiskFindingStatusResult(payload))
}

func validFindingStatus(status risk.FindingStatus) bool {
	return status == risk.FindingStatusKnown || status == risk.FindingStatusPendingReview || status == risk.FindingStatusActionNeeded
}

func (api *API) queryAssetDifferences(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	if api.analysis == nil {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "asset difference analysis is unavailable")
	}
	var payload AssetDifferenceQueryRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "asset difference query payload is invalid")
	}
	categories, err := normalizeAssetDifferenceCategories(payload.Categories)
	if err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "asset difference query payload is invalid")
	}
	release, err := api.analysis.acquire(ctx)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	defer release()
	differences, available, err := api.loadAssetDifferences(ctx, payload.SessionID)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeAssetDifferenceResult, AssetDifferenceResult{
		SessionID: payload.SessionID, BaselineAvailable: available,
		Differences: filterAssetDifferences(differences, categories),
	})
}

func (api *API) exportReport(ctx context.Context, request contracts.Message, client ClientIdentity) (contracts.Message, error) {
	if api.analysis == nil {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "report export is unavailable")
	}
	var payload ReportExportRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "report export payload is invalid")
	}
	if payload.Format != ReportFormatHTML && payload.Format != ReportFormatMarkdown && payload.Format != ReportFormatJSON {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "report format is invalid")
	}
	if payload.ToSequence > 0 && payload.FromSequence > payload.ToSequence {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "report sequence range is invalid")
	}
	release, err := api.analysis.acquire(ctx)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	defer release()
	records, totalEvents, statuses, err := api.readReportRangeWithFindingStatuses(ctx, payload)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	evaluation, err := api.evaluateRecords(ctx, payload.SessionID, records)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	for index := range evaluation.Findings {
		status := risk.FindingStatus(statuses[evaluation.Findings[index].ID])
		if validFindingStatus(status) {
			evaluation.Findings[index].Status = status
		}
	}
	session, err := api.analysis.store.GetSession(ctx, payload.SessionID)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	assetDifferences, _, err := api.loadAssetDifferences(ctx, payload.SessionID)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	events := make([]domain.AuditEvent, len(records))
	for index := range records {
		events[index] = records[index].Event
	}
	summary, err := reporting.BuildSessionSummary(session, events, evaluation, true)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	report, err := reporting.BuildReport(session, records, evaluation, summary, reporting.ReportOptions{
		Redaction: payload.Redaction, Now: api.now,
		MaximumEventCount: storage.MaximumReportRangeEvents,
		AssetDifferences:  assetDifferences,
	})
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	report.Range = &reporting.ReportRange{SessionEventCount: totalEvents, Partial: uint64(len(records)) != totalEvents}
	if len(records) > 0 {
		report.Range.FirstSequence = records[0].Event.Sequence
		report.Range.LastSequence = records[len(records)-1].Event.Sequence
	}
	var output bytes.Buffer
	mediaType := "text/html; charset=utf-8"
	switch payload.Format {
	case ReportFormatHTML:
		err = reporting.ExportHTML(report, &output)
	case ReportFormatMarkdown:
		mediaType = "text/markdown; charset=utf-8"
		err = reporting.ExportMarkdown(report, &output)
	case ReportFormatJSON:
		mediaType = "application/json"
		err = reporting.ExportJSON(report, &output)
	}
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	verification, err := reporting.NewVerificationManifest(report, output.Bytes(), mediaType)
	if err != nil {
		return api.analysisErrorResponse(request, err)
	}
	inline, err := api.response(request, contracts.MessageTypeReportResult, ReportResult{
		Format: payload.Format, MediaType: mediaType, Content: output.String(),
		Verification: &verification, GeneratedUTC: report.GeneratedUTC,
	})
	if err != nil {
		return contracts.Message{}, err
	}
	encoded, err := json.Marshal(inline)
	if err != nil {
		return contracts.Message{}, err
	}
	if len(encoded) > maximumInlineReportBytes {
		if api.analysis.exports == nil {
			return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "large report transfer is unavailable")
		}
		transfer, transferErr := api.analysis.exports.Put(output.Bytes(), client.Principal())
		if transferErr != nil {
			if errors.Is(transferErr, ErrExportCapacity) {
				return api.errorResponse(request, ErrorCodeReportTooLarge, "report exceeds the secure transfer capacity")
			}
			return api.analysisErrorResponse(request, transferErr)
		}
		return api.response(request, contracts.MessageTypeReportResult, ReportResult{
			Format: payload.Format, MediaType: mediaType, Verification: &verification,
			GeneratedUTC: report.GeneratedUTC,
			Transfer: &ReportTransfer{
				Token: transfer.Token, Size: transfer.Size, SHA256: transfer.SHA256,
				ChunkBytes: transfer.ChunkBytes, ExpiresUTC: transfer.ExpiresUTC,
			},
		})
	}
	return inline, nil
}

func (api *API) loadAssetDifferences(
	ctx context.Context,
	sessionID string,
) ([]domain.AssetDifference, bool, error) {
	store, ok := api.analysis.store.(assetBaselineStore)
	if !ok {
		return nil, false, nil
	}
	before, err := store.LoadAssetBaseline(ctx, sessionID, storage.AssetBaselineStageStart)
	if err != nil {
		if errors.Is(err, storage.ErrAssetBaselineNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	after, err := store.LoadAssetBaseline(ctx, sessionID, storage.AssetBaselineStageEnd)
	if err != nil {
		if errors.Is(err, storage.ErrAssetBaselineNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	differences, err := domain.CompareAssetBaselines(before, after)
	if err != nil {
		return nil, false, err
	}
	return differences, true, nil
}

func normalizeAssetDifferenceCategories(
	categories []domain.AssetCategory,
) (map[domain.AssetCategory]struct{}, error) {
	if len(categories) == 0 {
		return nil, nil
	}
	allowed := make(map[domain.AssetCategory]struct{}, len(categories))
	for _, category := range categories {
		switch category {
		case domain.AssetCategorySoftware, domain.AssetCategoryDevice,
			domain.AssetCategoryNetwork, domain.AssetCategoryAccount,
			domain.AssetCategorySystem:
			allowed[category] = struct{}{}
		default:
			return nil, errors.New("asset difference category is invalid")
		}
	}
	return allowed, nil
}

func filterAssetDifferences(
	differences []domain.AssetDifference,
	allowed map[domain.AssetCategory]struct{},
) []domain.AssetDifference {
	if len(allowed) == 0 {
		return append([]domain.AssetDifference(nil), differences...)
	}
	filtered := make([]domain.AssetDifference, 0, len(differences))
	for _, difference := range differences {
		asset := difference.After
		if asset == nil {
			asset = difference.Before
		}
		if asset == nil {
			continue
		}
		if _, includes := allowed[asset.Category]; includes {
			filtered = append(filtered, difference)
		}
	}
	return filtered
}

func (api *API) getReportChunk(request contracts.Message, client ClientIdentity) (contracts.Message, error) {
	if api.analysis == nil || api.analysis.exports == nil {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "large report transfer is unavailable")
	}
	var payload ReportChunkRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.Token) == "" || payload.Offset < 0 {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "report chunk payload is invalid")
	}
	chunk, err := api.analysis.exports.Read(payload.Token, payload.Offset, client.Principal())
	if err != nil {
		return api.errorResponse(request, ErrorCodeReportTransferInvalid, "report transfer is invalid or has expired")
	}
	return api.response(request, contracts.MessageTypeReportChunkResult, ReportChunkResult{
		Content: chunk.Content, NextOffset: chunk.NextOffset, Done: chunk.Done,
	})
}

func (api *API) evaluateStoredSession(ctx context.Context, sessionID string) (risk.Evaluation, []storage.EventRecord, error) {
	records, err := api.analysis.store.ListEvents(ctx, sessionID)
	if err != nil {
		return risk.Evaluation{}, nil, err
	}
	evaluation, err := api.evaluateRecords(ctx, sessionID, records)
	return evaluation, records, err
}

func (api *API) evaluateRecords(ctx context.Context, sessionID string, records []storage.EventRecord) (risk.Evaluation, error) {
	events := make([]risk.Event, len(records))
	for index, record := range records {
		events[index] = risk.Event{AuditEvent: record.Event, Payload: append([]byte(nil), record.Payload...)}
	}
	session, err := api.analysis.store.GetSession(ctx, sessionID)
	if err != nil {
		return risk.Evaluation{}, err
	}
	policy := session.MonitoringPolicy.Resolved()
	evaluator, err := risk.NewEvaluator(risk.RulesForPolicy(policy.RiskRules)...)
	if err != nil {
		return risk.Evaluation{}, err
	}
	evaluation, err := evaluator.Evaluate(ctx, sessionID, events)
	if err != nil {
		return risk.Evaluation{}, err
	}
	statuses := make(map[string]risk.FindingStatus)
	for _, record := range records {
		if record.Event.Action != "risk_finding_status_changed" {
			continue
		}
		var payload struct {
			FindingID string             `json:"findingId"`
			Status    risk.FindingStatus `json:"status"`
		}
		if json.Unmarshal(record.Payload, &payload) == nil && validFindingStatus(payload.Status) {
			statuses[payload.FindingID] = payload.Status
		}
	}
	for index := range evaluation.Findings {
		if status, exists := statuses[evaluation.Findings[index].ID]; exists {
			evaluation.Findings[index].Status = status
		}
	}
	return evaluation, nil
}

func (api *API) analysisErrorResponse(request contracts.Message, err error) (contracts.Message, error) {
	if errors.Is(err, storage.ErrEventRangeTooLarge) {
		return api.errorResponse(request, ErrorCodeReportTooLarge, "单次报告最多包含 100000 条事件，原始负载最多 32 MiB；请填写较小的起止序列范围分批导出。")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return api.errorResponse(request, ErrorCodeRequestExpired, "analysis request was canceled or timed out")
	}
	return api.errorResponse(request, ErrorCodeStorageFailure, "analysis request could not be completed")
}

func (api *API) readReportRangeWithFindingStatuses(ctx context.Context, request ReportExportRequest) ([]storage.EventRecord, uint64, map[string]string, error) {
	if store, ok := api.analysis.store.(interface {
		ListEventsRangeWithFindingStatuses(context.Context, string, uint64, uint64) ([]storage.EventRecord, uint64, map[string]string, error)
	}); ok {
		return store.ListEventsRangeWithFindingStatuses(ctx, request.SessionID, request.FromSequence, request.ToSequence)
	}
	// Compatibility stores provide the full session; derive both outputs from
	// that same read, so a status lookup cannot race the selected evidence.
	records, err := api.analysis.store.ListEvents(ctx, request.SessionID)
	if err != nil {
		return nil, 0, nil, err
	}
	selected := make([]storage.EventRecord, 0)
	statuses := make(map[string]string)
	for _, record := range records {
		if record.Event.Sequence >= request.FromSequence && (request.ToSequence == 0 || record.Event.Sequence <= request.ToSequence) {
			selected = append(selected, record)
		}
		if record.Event.Action == "risk_finding_status_changed" {
			var status struct {
				FindingID string `json:"findingId"`
				Status    string `json:"status"`
			}
			if json.Unmarshal(record.Payload, &status) == nil && validFindingStatus(risk.FindingStatus(status.Status)) {
				statuses[status.FindingID] = status.Status
			}
		}
	}
	if len(selected) > storage.MaximumReportRangeEvents {
		return nil, uint64(len(records)), nil, storage.ErrEventRangeTooLarge
	}
	return selected, uint64(len(records)), statuses, nil
}

func (runtime *analysisRuntime) acquire(ctx context.Context) (func(), error) {
	if runtime.slots == nil {
		return func() {}, nil
	}
	select {
	case runtime.slots <- struct{}{}:
		return func() { <-runtime.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
