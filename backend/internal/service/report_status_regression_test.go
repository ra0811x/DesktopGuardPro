package service

import (
	"context"
	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/reporting"
	"desktopguardpro/internal/risk"
	"encoding/json"
	"testing"
)

func TestRegressionPartialReportPreservesKnownStatus(t *testing.T) {
	api, now, store := newAnalysisTestAPI(t)
	evaluation, _, err := api.evaluateStoredSession(context.Background(), "session-1")
	if err != nil || len(evaluation.Findings) == 0 {
		t.Fatalf("setup evaluation=%+v err=%v", evaluation, err)
	}
	update, err := api.Handle(newTestMessage(t, contracts.MessageTypeRiskFindingStatusUpdate, now, RiskFindingStatusUpdateRequest{SessionID: "session-1", FindingID: evaluation.Findings[0].ID, Status: risk.FindingStatusKnown}))
	if err != nil || update.Type == contracts.MessageTypeError {
		t.Fatalf("setup update=%s err=%v", update.Payload, err)
	}
	if len(store.records) != 2 {
		t.Fatalf("setup records=%d", len(store.records))
	}
	response, err := api.Handle(newTestMessage(t, contracts.MessageTypeReportExport, now, ReportExportRequest{SessionID: "session-1", Format: ReportFormatJSON, FromSequence: 1, ToSequence: 1}))
	if err != nil || response.Type == contracts.MessageTypeError {
		t.Fatalf("export=%s err=%v", response.Payload, err)
	}
	var result ReportResult
	decodeTestPayload(t, response, &result)
	var report reporting.Report
	if err := json.Unmarshal([]byte(result.Content), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Status != risk.FindingStatusKnown {
		t.Fatalf("selected evidence lost its review status: findings=%+v", report.Findings)
	}
}
