package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/ipc"
	"desktopguardpro/internal/reporting"
	"desktopguardpro/internal/risk"
	"desktopguardpro/internal/storage"
)

type blockingAnalysisStore struct {
	*analysisTestStore
	deadlineSeen bool
}

func (store *blockingAnalysisStore) QueryTimeline(ctx context.Context, _ storage.TimelineQuery) (storage.TimelinePage, error) {
	_, store.deadlineSeen = ctx.Deadline()
	if !store.deadlineSeen {
		return storage.TimelinePage{}, errors.New("query has no cancellation deadline")
	}
	<-ctx.Done()
	return storage.TimelinePage{}, ctx.Err()
}

func TestAnalysisQueryHasBoundedRequestLifetime(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	blocking := &blockingAnalysisStore{analysisTestStore: store}
	api.analysis.store = blocking
	request := newTestMessage(t, contracts.MessageTypeTimelineQuery, now, TimelineQueryRequest{SessionID: "session-1"})
	request.DeadlineUTC = now.Add(20 * time.Millisecond)
	started := time.Now()
	response, err := api.Handle(request)
	if err != nil {
		t.Fatal(err)
	}
	if !blocking.deadlineSeen {
		t.Fatal("analysis continued without the client's deadline")
	}
	if time.Since(started) > time.Second {
		t.Fatal("expired query kept resources")
	}
	assertAPIError(t, response, ErrorCodeRequestExpired)
}

func TestRiskCoverageIncludesGapsBeyondFirstTimelinePage(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	base := store.records[0].Event
	store.records = nil
	for sequence := 1; sequence <= 101; sequence++ {
		event := base
		event.Sequence = uint64(sequence)
		event.EventID = fmt.Sprintf("event-%d", sequence)
		event.Category, event.Action = domain.EventCategoryProcess, "process_started"
		if sequence == 101 {
			event.Category, event.Action = domain.EventCategoryHealth, "observation_queue_overflow"
		}
		store.records = append(store.records, storage.EventRecord{Event: event, Payload: []byte(`{}`)})
	}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeRiskEvaluate, now, RiskEvaluateRequest{SessionID: "session-1"}))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		EventCount       uint64 `json:"eventCount"`
		CoverageGapCount uint64 `json:"coverageGapCount"`
	}
	decodeTestPayload(t, response, &result)
	if result.EventCount != 101 || result.CoverageGapCount != 1 {
		t.Fatalf("incomplete session coverage: %+v", result)
	}
}

func TestHTMLReportTransferUsesFinalJSONFrameSize(t *testing.T) {
	for _, value := range []string{strings.Repeat("<", 150_000), strings.Repeat("\\", 190_000), strings.Repeat("\n", 190_000), strings.Repeat("a", 780_000)} {
		api, now, store := newAnalysisTestAPI(t)
		var payload bytes.Buffer
		encoder := json.NewEncoder(&payload)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(map[string]string{"value": value}); err != nil {
			t.Fatal(err)
		}
		store.records[0].Payload = payload.Bytes()
		response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, ReportExportRequest{
			SessionID: "session-1", Format: ReportFormatHTML, Redaction: reporting.RedactionPolicy{IncludePayload: true},
		}))
		if err != nil || response.Type != contracts.MessageTypeReportResult {
			t.Fatalf("report: %s, %v", response.Type, err)
		}
		var framed bytes.Buffer
		if err := ipc.WriteMessage(&framed, response); err != nil {
			t.Fatalf("HTML response exceeded final frame: %v", err)
		}
	}
}

func TestAPIExportsJSONReport(t *testing.T) {
	api, now, _ := newAnalysisTestAPI(t)
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, ReportExportRequest{
		SessionID: "session-1", Format: ReportFormatJSON,
	}))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	var result ReportResult
	decodeTestPayload(t, response, &result)
	if response.Type != contracts.MessageTypeReportResult || result.MediaType != "application/json" {
		t.Fatalf("report result = %+v", result)
	}
	if result.Verification == nil {
		t.Fatal("JSON report result omitted its verification manifest")
	}
	if err := reporting.VerifyReportContent(*result.Verification, []byte(result.Content)); err != nil {
		t.Fatalf("verification manifest error = %v", err)
	}
	var report reporting.Report
	if err := json.Unmarshal([]byte(result.Content), &report); err != nil || report.Session.ID != "session-1" {
		t.Fatalf("JSON report = %+v, error = %v", report, err)
	}
}

func TestAPITimelineQuery(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeTimelineQuery, now, TimelineQueryRequest{
		SessionID: "session-1", Limit: 25, Categories: []domain.EventCategory{domain.EventCategoryProcess},
		Severities: []domain.EventSeverity{domain.EventSeverityHigh}, UserSIDHash: strings.Repeat("a", 64),
		Process: "editor", Path: `Evidence\report`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != contracts.MessageTypeTimelineResult || store.timelineQuery.Limit != 25 ||
		store.timelineQuery.Categories[0] != domain.EventCategoryProcess ||
		store.timelineQuery.Severities[0] != domain.EventSeverityHigh || store.timelineQuery.Process != "editor" ||
		store.timelineQuery.Path != `Evidence\report` || store.timelineQuery.UserSIDHash != strings.Repeat("a", 64) {
		t.Fatalf("unexpected timeline response or query: %q %+v", response.Type, store.timelineQuery)
	}
}

func TestAPIQueriesAssetDifferencesWithCategoryFilter(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	beforeSoftware := domain.Asset{
		Category: domain.AssetCategorySoftware, Identifier: "software-package-1",
		DisplayName: "Example Tool", Attributes: map[string]string{"version": "1.0"},
	}
	afterSoftware := beforeSoftware
	afterSoftware.Attributes = map[string]string{"version": "2.0"}
	store.assetBaselines = map[storage.AssetBaselineStage]domain.AssetBaseline{
		storage.AssetBaselineStageStart: {Assets: []domain.Asset{beforeSoftware}},
		storage.AssetBaselineStageEnd: {Assets: []domain.Asset{
			afterSoftware,
			{
				Category: domain.AssetCategoryDevice, Identifier: `USB\VID_1234`,
				DisplayName: "Added USB Device",
			},
		}},
	}

	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeAssetDifferenceQuery, now,
		AssetDifferenceQueryRequest{
			SessionID: "session-1", Categories: []domain.AssetCategory{domain.AssetCategorySoftware},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != contracts.MessageTypeAssetDifferenceResult {
		t.Fatalf("response type = %q", response.Type)
	}
	var result AssetDifferenceResult
	decodeTestPayload(t, response, &result)
	if !result.BaselineAvailable || len(result.Differences) != 1 {
		t.Fatalf("asset difference result = %+v", result)
	}
	difference := result.Differences[0]
	if difference.Kind != domain.AssetDifferenceChanged || difference.After == nil ||
		difference.After.Category != domain.AssetCategorySoftware ||
		difference.After.Identifier != "software-package-1" {
		t.Fatalf("unexpected filtered difference: %+v", difference)
	}

	invalid, err := api.Handle(newTestMessage(t, contracts.MessageTypeAssetDifferenceQuery, now,
		AssetDifferenceQueryRequest{SessionID: "session-1", Categories: []domain.AssetCategory{"unknown"}}))
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, invalid, ErrorCodeInvalidPayload)
}

func TestReportExportPassesTenThousandEventBoundary(t *testing.T) {
	for _, count := range []int{9_999, 10_000, 10_001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			api, now, store := newAnalysisTestAPI(t)
			base := store.records[0].Event
			store.records = make([]storage.EventRecord, count)
			for i := range store.records {
				event := base
				event.EventID, event.Sequence = fmt.Sprintf("export-%d", i+1), uint64(i+1)
				event.Category, event.Action = domain.EventCategoryFile, "file_created"
				store.records[i] = storage.EventRecord{Event: event, Payload: []byte(`{}`)}
			}
			response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, ReportExportRequest{SessionID: "session-1", Format: ReportFormatMarkdown}))
			if err != nil || response.Type != contracts.MessageTypeReportResult {
				t.Fatalf("%d event export failed: %s %s %v", count, response.Type, response.Payload, err)
			}
			var framed bytes.Buffer
			if err := ipc.WriteMessage(&framed, response); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOversizedReportOffersWorkingSequenceRange(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	base := store.records[0].Event
	store.records = make([]storage.EventRecord, 100_001)
	for i := range store.records {
		event := base
		event.EventID, event.Sequence = fmt.Sprintf("range-%d", i+1), uint64(i+1)
		event.Category, event.Action = domain.EventCategoryFile, "file_created"
		store.records[i] = storage.EventRecord{Event: event, Payload: []byte(`{}`)}
	}
	request := ReportExportRequest{SessionID: "session-1", Format: ReportFormatMarkdown}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, request))
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, ErrorCodeReportTooLarge)
	request.FromSequence, request.ToSequence = 5001, 5100
	response, err = api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, request))
	if err != nil || response.Type != contracts.MessageTypeReportResult {
		t.Fatalf("range export: %v %s", err, response.Payload)
	}
	var report ReportResult
	decodeTestPayload(t, response, &report)
	if !strings.Contains(report.Content, "序列 5001–5100") || !strings.Contains(report.Content, "会话共 100001") {
		t.Fatal("range report does not identify its selection")
	}
}

func TestAPIRiskEvaluation(t *testing.T) {
	api, now, _ := newAnalysisTestAPI(t)
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeRiskEvaluate, now, RiskEvaluateRequest{SessionID: "session-1"}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != contracts.MessageTypeRiskResult {
		t.Fatalf("response type = %q", response.Type)
	}
	var result risk.Evaluation
	decodeTestPayload(t, response, &result)
	if len(result.Findings) != 1 || result.Findings[0].RuleID != "device.connection" {
		t.Fatalf("unexpected evaluation: %+v", result)
	}
}

func TestAPIRiskFindingStatusPersistsAsAuditEventAndAppliesToEvaluation(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	initial, _, err := api.evaluateStoredSession(context.Background(), "session-1")
	if err != nil || len(initial.Findings) != 1 {
		t.Fatalf("initial evaluation=%+v error=%v", initial, err)
	}
	request := RiskFindingStatusUpdateRequest{
		SessionID: "session-1", FindingID: initial.Findings[0].ID, Status: risk.FindingStatusKnown,
	}
	client := ClientIdentity{UserSID: "S-1-5-21-1000", WindowsSessionID: 7}
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeRiskFindingStatusUpdate, now, request), client)
	if err != nil || response.Type != contracts.MessageTypeRiskFindingStatusResult {
		t.Fatalf("status update response=%+v error=%v", response, err)
	}
	if len(store.records) != 2 || store.records[1].Event.Action != "risk_finding_status_changed" {
		t.Fatalf("stored records=%#v", store.records)
	}
	wantSIDHash := sha256.Sum256([]byte(client.UserSID))
	if !bytes.Equal(store.records[1].Event.UserSIDHash, wantSIDHash[:]) ||
		store.records[1].Event.WindowsSessionID == nil || *store.records[1].Event.WindowsSessionID != client.WindowsSessionID {
		t.Fatalf("status operator attribution=%+v", store.records[1].Event)
	}
	evaluation, _, err := api.evaluateStoredSession(context.Background(), "session-1")
	if err != nil || evaluation.Findings[0].Status != risk.FindingStatusKnown {
		t.Fatalf("updated evaluation=%+v error=%v", evaluation, err)
	}
}

func TestAPIExportsEscapedHTMLReport(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	store.session.Name = `<script>alert(1)</script>`
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, ReportExportRequest{
		SessionID: "session-1", Format: ReportFormatHTML,
		Redaction: reporting.RedactionPolicy{ObjectDetails: reporting.ObjectDetailBasename},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != contracts.MessageTypeReportResult {
		t.Fatalf("response type = %q", response.Type)
	}
	var result ReportResult
	decodeTestPayload(t, response, &result)
	if strings.Contains(result.Content, "<script>alert") || !strings.Contains(result.Content, "&lt;script&gt;") {
		t.Fatal("report content was not escaped")
	}
}

func TestReportExportIncludesStoredAssetChanges(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	beforeSoftware := domain.Asset{
		Category: domain.AssetCategorySoftware, Identifier: "software-package-1",
		DisplayName: "Example Tool", Attributes: map[string]string{"version": "1.0"},
	}
	afterSoftware := beforeSoftware
	afterSoftware.Attributes = map[string]string{"version": "2.0"}
	store.assetBaselines = map[storage.AssetBaselineStage]domain.AssetBaseline{
		storage.AssetBaselineStageStart: {Assets: []domain.Asset{beforeSoftware}},
		storage.AssetBaselineStageEnd: {Assets: []domain.Asset{
			afterSoftware,
			{
				Category: domain.AssetCategoryDevice, Identifier: `USB\VID_1234`,
				DisplayName: "Added USB Device",
			},
		}},
	}

	response, err := api.Handle(newTestMessage(
		t,
		contracts.MessageTypeReportExport,
		now,
		ReportExportRequest{
			SessionID: "session-1",
			Format:    ReportFormatMarkdown,
			Redaction: reporting.RedactionPolicy{
				ObjectDetails: reporting.ObjectDetailFull,
			},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	var result ReportResult
	decodeTestPayload(t, response, &result)
	for _, required := range []string{
		"## 资产变化", "新增", "变更", "Added USB Device",
		"software-package-1", "version",
	} {
		if !strings.Contains(result.Content, required) {
			t.Fatalf("report does not contain %q: %s", required, result.Content)
		}
	}
}

func TestAPIReportsAnalysisUnavailable(t *testing.T) {
	api, now := newTestAPI()
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeRiskEvaluate, now, RiskEvaluateRequest{SessionID: "session-1"}))
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, ErrorCodeAnalysisUnavailable)
}

func TestAPIDoesNotExposeAnalysisStorageDetails(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	store.listError = errors.New(`open C:\ProgramData\DesktopGuardPro\desktop-guard.db: access denied`)
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeRiskEvaluate, now, RiskEvaluateRequest{SessionID: "session-1"}))
	if err != nil {
		t.Fatal(err)
	}
	var result ErrorResult
	decodeTestPayload(t, response, &result)
	if strings.Contains(result.Message, "ProgramData") || strings.Contains(result.Message, "desktop-guard.db") {
		t.Fatalf("error response exposed storage details: %q", result.Message)
	}
}

func TestAPITransfersLargeReportInVerifiedChunks(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	store.records[0].Payload = []byte(`{"data":"` + strings.Repeat("a", maximumInlineReportBytes) + `"}`)
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, ReportExportRequest{
		SessionID: "session-1", Format: ReportFormatMarkdown,
		Redaction: reporting.RedactionPolicy{
			ObjectDetails: reporting.ObjectDetailFull, IncludePayload: true, MaximumTextLength: 512,
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var result ReportResult
	decodeTestPayload(t, response, &result)
	if result.Content != "" || result.Transfer == nil || result.Transfer.Size <= maximumInlineReportBytes {
		t.Fatalf("unexpected large report delivery: %+v", result)
	}

	content := make([]byte, 0, result.Transfer.Size)
	offset := 0
	for {
		chunkResponse, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportChunkGet, now, ReportChunkRequest{
			Token: result.Transfer.Token, Offset: offset,
		}))
		if err != nil {
			t.Fatal(err)
		}
		var framed bytes.Buffer
		if err := ipc.WriteMessage(&framed, chunkResponse); err != nil {
			t.Fatalf("chunk response exceeded IPC frame: %v", err)
		}
		var chunk ReportChunkResult
		decodeTestPayload(t, chunkResponse, &chunk)
		content = append(content, chunk.Content...)
		offset = chunk.NextOffset
		if chunk.Done {
			break
		}
	}
	digest := sha256.Sum256(content)
	if len(content) != result.Transfer.Size || hex.EncodeToString(digest[:]) != result.Transfer.SHA256 {
		t.Fatal("reassembled report failed size or digest verification")
	}
	reused, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportChunkGet, now, ReportChunkRequest{
		Token: result.Transfer.Token, Offset: offset,
	}))
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, reused, ErrorCodeReportTransferInvalid)
}

type analysisTestStore struct {
	session        domain.Session
	records        []storage.EventRecord
	timelineQuery  storage.TimelineQuery
	listError      error
	assetBaselines map[storage.AssetBaselineStage]domain.AssetBaseline
}

func (store *analysisTestStore) CreateSession(context.Context, domain.Session, time.Time) error {
	return nil
}
func (store *analysisTestStore) UpdateSession(context.Context, domain.Session, time.Time) error {
	return nil
}

func (store *analysisTestStore) GetSession(context.Context, string) (domain.Session, error) {
	return store.session, nil
}

func (store *analysisTestStore) ListEvents(context.Context, string) ([]storage.EventRecord, error) {
	if store.listError != nil {
		return nil, store.listError
	}
	return append([]storage.EventRecord(nil), store.records...), nil
}

func (store *analysisTestStore) LoadAssetBaseline(
	_ context.Context,
	_ string,
	stage storage.AssetBaselineStage,
) (domain.AssetBaseline, error) {
	baseline, exists := store.assetBaselines[stage]
	if !exists {
		return domain.AssetBaseline{}, storage.ErrAssetBaselineNotFound
	}
	return baseline, nil
}

func (store *analysisTestStore) QueryTimeline(_ context.Context, query storage.TimelineQuery) (storage.TimelinePage, error) {
	store.timelineQuery = query
	return storage.TimelinePage{Records: store.records, IntegrityVerified: true}, nil
}

func (store *analysisTestStore) AppendEventAutoSequence(_ context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error) {
	event.Sequence = uint64(len(store.records) + 1)
	store.records = append(store.records, storage.EventRecord{Event: event, Payload: append([]byte(nil), payload...)})
	return event, nil
}

func newAnalysisTestAPI(t *testing.T) (*API, time.Time, *analysisTestStore) {
	t.Helper()
	now := time.Date(2026, 8, 23, 13, 0, 0, 0, time.UTC)
	event := domain.AuditEvent{
		EventID: "event-1", SessionID: "session-1", Sequence: 1,
		Category: domain.EventCategoryDevice, Action: "device_connected",
		Severity: domain.EventSeverityMedium, ObservedUTC: now.Add(-time.Minute),
		ObjectKey: `USB\VID_1234`, Source: "windows_device_snapshot",
		Confidence: domain.EventConfidenceDirect,
	}
	store := &analysisTestStore{
		session: domain.Session{ID: "session-1", Name: "Analysis test", State: domain.SessionStateCompleted, Revision: 4},
		records: []storage.EventRecord{{Event: event, Payload: []byte(`{"instanceId":"USB\\VID_1234"}`)}},
	}
	evaluator, err := risk.NewEvaluator(risk.DefaultRules()...)
	if err != nil {
		t.Fatal(err)
	}
	exports, err := newExportStore(exportStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{
		coordinator: NewCoordinator(), store: store,
		analysis: &analysisRuntime{store: store, evaluator: evaluator, exports: exports}, now: func() time.Time { return now },
	}
	return api, now, store
}
