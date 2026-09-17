package reporting

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/risk"
	"desktopguardpro/internal/storage"
)

func TestBuildReportAppliesDefaultRedaction(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	report, err := BuildReport(session, records, evaluation, summary, ReportOptions{Now: reportTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Timeline) != 1 || report.Timeline[0].ObjectKey != "secret.txt" ||
		report.Timeline[0].ProcessKey != "" || len(report.Timeline[0].Payload) != 0 {
		t.Fatalf("default redaction failed: %+v", report.Timeline)
	}
	if report.Findings[0].Evidence[0].ObjectKey != "secret.txt" {
		t.Fatalf("finding evidence was not redacted: %+v", report.Findings[0].Evidence)
	}
	if !report.GeneratedUTC.Equal(reportTestNow()) || report.SchemaVersion != 1 || report.SoftwareVersion != "development" {
		t.Fatalf("unexpected report metadata: %+v", report)
	}
}

func TestBuildReportIncludesExplicitSoftwareVersion(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	report, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now: reportTestNow, SoftwareVersion: "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SoftwareVersion != "1.2.3" {
		t.Fatalf("software version = %q", report.SoftwareVersion)
	}
}

func TestBuildReportIncludesExplicitFullDetails(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	report, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now: reportTestNow,
		Redaction: RedactionPolicy{
			ObjectDetails: ObjectDetailFull, IncludeProcessKey: true, IncludePayload: true,
			MaximumTextLength: 512,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Timeline[0].ObjectKey != records[0].Event.ObjectKey ||
		report.Timeline[0].ProcessKey != records[0].Event.ProcessKey ||
		!bytes.Equal(report.Timeline[0].Payload, records[0].Payload) {
		t.Fatalf("explicit details were not retained: %+v", report.Timeline[0])
	}
	report.Timeline[0].Payload[0] = 'x'
	if records[0].Payload[0] == 'x' {
		t.Fatal("report payload aliases repository data")
	}
}

func TestBuildReportRedactsPayloadUserWindowTitleAndPathIndependently(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	records[0].Payload = []byte(`{"userName":"Raymond","windowTitle":"客户名单.xlsx","processPath":"C:\\Apps\\editor.exe"}`)
	report, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Redaction: RedactionPolicy{ObjectDetails: ObjectDetailBasename, IncludePayload: true},
		Now:       func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := string(report.Timeline[0].Payload)
	if strings.Contains(payload, "Raymond") || strings.Contains(payload, "客户名单") || strings.Contains(payload, `C:\\Apps`) ||
		!strings.Contains(payload, "editor.exe") {
		t.Fatalf("redacted payload = %s", payload)
	}
}

func TestBuildReportRejectsInvalidIncludedPayload(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	records[0].Payload = []byte("invalid json")
	_, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now:       reportTestNow,
		Redaction: RedactionPolicy{ObjectDetails: ObjectDetailOmit, IncludePayload: true},
	})
	if !errors.Is(err, ErrReportInputInvalid) {
		t.Fatalf("expected invalid report input, got %v", err)
	}
}

func TestBuildReportRejectsInconsistentSummary(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	summary.EventCount++
	_, err := BuildReport(session, records, evaluation, summary, ReportOptions{Now: reportTestNow})
	if !errors.Is(err, ErrReportInputInvalid) {
		t.Fatalf("expected inconsistent summary error, got %v", err)
	}
}

func reportTestInputs(t *testing.T) (domain.Session, []storage.EventRecord, risk.Evaluation, SessionSummary) {
	t.Helper()
	session := summarySession()
	event := summaryEvent("event-1", 1, domain.EventCategoryFile, "created")
	event.ObjectKey = `C:\Users\Raymond\Documents\secret.txt`
	event.ProcessKey = "123:456"
	records := []storage.EventRecord{{Event: event, Payload: []byte(`{"path":"C:\\Users\\Raymond\\Documents\\secret.txt"}`)}}
	evaluation := risk.Evaluation{SessionID: session.ID, Findings: []risk.Finding{summaryFinding(session.ID, risk.LevelMedium, event)}}
	summary, err := BuildSessionSummary(session, []domain.AuditEvent{event}, evaluation, true)
	if err != nil {
		t.Fatal(err)
	}
	return session, records, evaluation, summary
}

func reportTestNow() time.Time {
	return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
}
