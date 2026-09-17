package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/risk"
)

func TestBuildSessionSummaryAggregatesRiskAndCoverage(t *testing.T) {
	session := summarySession()
	process := summaryEvent("event-2", 2, domain.EventCategoryProcess, "process_started")
	gap := summaryEvent("event-1", 1, domain.EventCategoryHealth, "observation_queue_overflow")
	evaluation := risk.Evaluation{SessionID: session.ID, Findings: []risk.Finding{
		summaryFinding(session.ID, risk.LevelMedium, process),
		summaryFinding(session.ID, risk.LevelHigh, gap),
	}, Failures: []risk.RuleFailure{{RuleID: "failed", Message: "failure"}}}

	summary, err := BuildSessionSummary(session, []domain.AuditEvent{process, gap}, evaluation, true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.EventCount != 2 || summary.EventsByCategory[domain.EventCategoryHealth] != 1 ||
		summary.FindingCount != 2 || summary.FindingsByLevel[risk.LevelHigh] != 1 ||
		summary.HighestRisk != risk.LevelHigh {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
	if summary.CollectorHealth != HealthStatusDegraded || len(summary.CoverageGaps) != 1 || summary.RuleFailureCount != 1 {
		t.Fatalf("unexpected health summary: %+v", summary)
	}
	if summary.FirstObservedUTC == nil || !summary.FirstObservedUTC.Equal(gap.ObservedUTC) ||
		summary.LastObservedUTC == nil || !summary.LastObservedUTC.Equal(process.ObservedUTC) {
		t.Fatal("unexpected observed time range")
	}
}

func TestBuildSessionSummaryHandlesEmptyVerifiedSession(t *testing.T) {
	session := summarySession()
	summary, err := BuildSessionSummary(session, nil, risk.Evaluation{SessionID: session.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.EventCount != 0 || summary.FirstObservedUTC != nil || summary.LastObservedUTC != nil ||
		summary.CollectorHealth != HealthStatusHealthy {
		t.Fatalf("unexpected empty summary: %+v", summary)
	}
}

func TestRecoveredInterruptionRemainsCoverageGapAfterSessionCompletes(t *testing.T) {
	session := summarySession()
	event := summaryEvent("recovery-1", 1, domain.EventCategoryHealth, "service_recovered_after_interruption")
	evaluator, err := risk.NewEvaluator(risk.CollectionGapRule{})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := evaluator.Evaluate(context.Background(), session.ID, []risk.Event{{AuditEvent: event}})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.CoverageGapCount != 1 || len(evaluation.Findings) != 1 {
		t.Errorf("recovered interruption missing from risk evaluation: %+v", evaluation)
	}
	if len(evaluation.Findings) == 1 && !strings.Contains(evaluation.Findings[0].Summary, "interruption") {
		t.Errorf("recovery finding does not explain the interruption: %q", evaluation.Findings[0].Summary)
	}
	summary, err := BuildSessionSummary(session, []domain.AuditEvent{event}, evaluation, true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CollectorHealth != HealthStatusDegraded || len(summary.CoverageGaps) != 1 {
		t.Fatalf("completed session hides its interruption: %+v", summary)
	}
}

func TestBuildSessionSummaryAcceptsPausedSession(t *testing.T) {
	session := summarySession()
	session.State = domain.SessionStatePaused
	summary, err := BuildSessionSummary(session, nil, risk.Evaluation{SessionID: session.ID}, true)
	if err != nil {
		t.Fatalf("paused session summary error = %v", err)
	}
	if summary.SessionState != domain.SessionStatePaused {
		t.Fatalf("summary state = %q", summary.SessionState)
	}
}

func TestSleepRecoveryIsReportedAsCoverageGap(t *testing.T) {
	session := summarySession()
	event := summaryEvent("sleep-1", 1, domain.EventCategoryHealth, "service_recovered_after_sleep")
	evaluation := risk.Evaluation{SessionID: session.ID}
	summary, err := BuildSessionSummary(session, []domain.AuditEvent{event}, evaluation, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.CoverageGaps) != 1 || summary.CollectorHealth != HealthStatusDegraded {
		t.Fatalf("sleep recovery summary = %+v", summary)
	}
}

func TestBuildSessionSummaryRecognizesStorageAndHashCoverageGaps(t *testing.T) {
	session := summarySession()
	events := []domain.AuditEvent{
		summaryEvent("usn-gap", 1, domain.EventCategoryHealth, "usn_journal_gap_detected"),
		summaryEvent("hash-gap", 2, domain.EventCategoryHealth, "file_hash_queue_overflow"),
		summaryEvent("asset-gap", 3, domain.EventCategoryHealth, "system_asset_snapshot_unavailable"),
		summaryEvent("security-log-gap", 4, domain.EventCategoryHealth, "security_log_monitor_unavailable"),
	}
	summary, err := BuildSessionSummary(session, events, risk.Evaluation{SessionID: session.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CollectorHealth != HealthStatusDegraded || len(summary.CoverageGaps) != 4 {
		t.Fatalf("summary does not retain new coverage gaps: %+v", summary)
	}
}

func TestBuildSessionSummaryRejectsCrossSessionEvent(t *testing.T) {
	session := summarySession()
	event := summaryEvent("event-1", 1, domain.EventCategoryFile, "created")
	event.SessionID = "another-session"
	_, err := BuildSessionSummary(session, []domain.AuditEvent{event}, risk.Evaluation{SessionID: session.ID}, true)
	if !errors.Is(err, ErrSummaryInputInvalid) {
		t.Fatalf("expected invalid summary input, got %v", err)
	}
}

func summarySession() domain.Session {
	return domain.Session{ID: "session-1", Name: "Summary test", State: domain.SessionStateCompleted, Revision: 4}
}

func summaryEvent(id string, sequence uint64, category domain.EventCategory, action string) domain.AuditEvent {
	return domain.AuditEvent{
		EventID: id, SessionID: "session-1", Sequence: sequence, Category: category,
		Action: action, Severity: domain.EventSeverityLow,
		ObservedUTC: time.Date(2026, 8, 23, 11, 0, int(sequence), 0, time.UTC),
		Source:      "test", Confidence: domain.EventConfidenceDirect,
	}
}

func summaryFinding(sessionID string, level risk.Level, event domain.AuditEvent) risk.Finding {
	return risk.Finding{
		ID: "finding-" + event.EventID, RuleID: "test.rule", Key: event.EventID,
		SessionID: sessionID, Title: "Finding", Summary: "Finding summary",
		Level: level, Score: 70, Confidence: 0.9,
		FirstObservedUTC: event.ObservedUTC, LastObservedUTC: event.ObservedUTC,
		Evidence: []risk.EvidenceReference{risk.EvidenceFromEvent(event)},
	}
}
