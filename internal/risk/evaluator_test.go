package risk

import (
	"context"
	"errors"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestEvaluatorNormalizesSortsAndCreatesStableIDs(t *testing.T) {
	events := []Event{testRiskEvent("event-2", 2), testRiskEvent("event-1", 1)}
	low := staticRule{id: "low-rule", findings: []Finding{testFinding("low", LevelLow, 30, events[0].AuditEvent)}}
	high := staticRule{id: "high-rule", findings: []Finding{testFinding("high", LevelHigh, 80, events[1].AuditEvent)}}
	evaluator, err := NewEvaluator(low, high)
	if err != nil {
		t.Fatal(err)
	}

	first, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.Evaluate(context.Background(), "session-1", events)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Findings) != 2 || first.Findings[0].RuleID != "high-rule" {
		t.Fatalf("unexpected finding order: %+v", first.Findings)
	}
	if first.Findings[0].ID == "" || first.Findings[0].ID != second.Findings[0].ID {
		t.Fatalf("finding id is not stable: %q and %q", first.Findings[0].ID, second.Findings[0].ID)
	}
	if first.Findings[0].Evidence[0].Action != events[1].AuditEvent.Action {
		t.Fatal("evidence was not normalized from the evaluated event")
	}
	if first.Findings[0].Status != FindingStatusPendingReview {
		t.Fatalf("default finding status = %q", first.Findings[0].Status)
	}
}

func TestEvaluatorIsolatesRuleErrorAndPanic(t *testing.T) {
	event := testRiskEvent("event-1", 1)
	good := staticRule{id: "good", findings: []Finding{testFinding("good", LevelMedium, 60, event.AuditEvent)}}
	failed := staticRule{id: "failed", err: errors.New("rule failed")}
	panicked := staticRule{id: "panicked", panicValue: "bad state"}
	evaluator, err := NewEvaluator(failed, good, panicked)
	if err != nil {
		t.Fatal(err)

	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].RuleID != "good" {
		t.Fatalf("unexpected findings: %+v", result.Findings)
	}
	if len(result.Failures) != 2 || result.Failures[0].RuleID != "failed" || result.Failures[1].RuleID != "panicked" {
		t.Fatalf("unexpected failures: %+v", result.Failures)
	}
}

func TestEvaluatorRejectsUnknownEvidenceAsRuleFailure(t *testing.T) {
	event := testRiskEvent("event-1", 1)
	finding := testFinding("unknown", LevelHigh, 90, event.AuditEvent)
	finding.Evidence[0].EventID = "missing"
	evaluator, err := NewEvaluator(staticRule{id: "invalid-evidence", findings: []Finding{finding}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 || len(result.Failures) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !errors.Is(errors.New(result.Failures[0].Message), ErrEvidenceNotFound) && result.Failures[0].Message == "" {
		t.Fatal("expected evidence failure message")
	}
}

func TestEvaluatorDoesNotMutateRuleFindings(t *testing.T) {
	event := testRiskEvent("event-1", 1)
	finding := testFinding("immutable", LevelLow, 20, event.AuditEvent)
	finding.Evidence[0].Action = "rule-owned-value"
	rule := staticRule{id: "immutable", findings: []Finding{finding}}
	evaluator, err := NewEvaluator(rule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event}); err != nil {
		t.Fatal(err)
	}
	if rule.findings[0].Evidence[0].Action != "rule-owned-value" {
		t.Fatal("evaluator mutated rule-owned finding data")
	}
}

func TestEvaluatorRecordsVersionsForVersionedRules(t *testing.T) {
	event := testRiskEvent("event-1", 1)
	evaluator, err := NewEvaluator(versionedStaticRule{
		staticRule: staticRule{id: "versioned-rule", findings: []Finding{
			testFinding("versioned", LevelLow, 20, event.AuditEvent),
		}},
		version: "2026.09.07.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", []Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RuleVersions) != 1 || result.RuleVersions[0].RuleID != "versioned-rule" ||
		result.RuleVersions[0].Version != "2026.09.07.1" {
		t.Fatalf("rule versions = %#v", result.RuleVersions)
	}
}

func TestNewEvaluatorRejectsDuplicateRuleIDs(t *testing.T) {
	_, err := NewEvaluator(staticRule{id: "duplicate"}, staticRule{id: "duplicate"})
	if !errors.Is(err, ErrRuleDuplicate) {
		t.Fatalf("expected duplicate rule error, got %v", err)
	}
}

func TestEvaluatorAllowsAllRiskRulesToBeDisabled(t *testing.T) {
	evaluator, err := NewEvaluator()
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}
	result, err := evaluator.Evaluate(context.Background(), "session-1", nil)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.SessionID != "session-1" || len(result.Findings) != 0 || len(result.RuleVersions) != 0 {
		t.Fatalf("evaluation = %+v", result)
	}
}

type staticRule struct {
	id         string
	findings   []Finding
	err        error
	panicValue any
}

type versionedStaticRule struct {
	staticRule
	version string
}

func (rule versionedStaticRule) Version() string { return rule.version }

func (rule staticRule) ID() string { return rule.id }

func (rule staticRule) Evaluate(context.Context, []Event) ([]Finding, error) {
	if rule.panicValue != nil {
		panic(rule.panicValue)
	}
	return append([]Finding(nil), rule.findings...), rule.err
}

func testRiskEvent(id string, sequence uint64) Event {
	return Event{AuditEvent: domain.AuditEvent{
		EventID: id, SessionID: "session-1", Sequence: sequence,
		Category: domain.EventCategoryProcess, Action: "process_started",
		Severity: domain.EventSeverityLow, ObservedUTC: time.Date(2026, 8, 23, 8, 0, int(sequence), 0, time.UTC),
		Source: "test", Confidence: domain.EventConfidenceDirect,
	}}
}

func testFinding(key string, level Level, score uint8, event domain.AuditEvent) Finding {
	return Finding{
		Key: key, Title: "Test finding", Summary: "Test summary", Level: level,
		Score: score, Confidence: 0.9, FirstObservedUTC: event.ObservedUTC,
		LastObservedUTC: event.ObservedUTC, Evidence: []EvidenceReference{EvidenceFromEvent(event)},
		Tags: []string{" test ", "test"},
	}
}
