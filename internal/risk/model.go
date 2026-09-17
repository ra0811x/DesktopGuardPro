package risk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

var (
	ErrRuleIDRequired    = errors.New("risk rule id is required")
	ErrRuleDuplicate     = errors.New("risk rule ids must be unique")
	ErrFindingInvalid    = errors.New("risk finding is invalid")
	ErrEvidenceInvalid   = errors.New("risk evidence reference is invalid")
	ErrEvaluationInvalid = errors.New("risk evaluation input is invalid")
	ErrEvidenceNotFound  = errors.New("risk evidence event was not found")
	ErrDuplicateFinding  = errors.New("risk finding keys must be unique within a rule")
	ErrRiskRuleRequired  = errors.New("at least one risk rule is required")
)

type Level string

type FindingStatus string

const (
	LevelInformational Level = "informational"
	LevelLow           Level = "low"
	LevelMedium        Level = "medium"
	LevelHigh          Level = "high"
	LevelCritical      Level = "critical"
)

const (
	FindingStatusKnown         FindingStatus = "known"
	FindingStatusPendingReview FindingStatus = "pending_review"
	FindingStatusActionNeeded  FindingStatus = "action_needed"
)

type Event struct {
	AuditEvent domain.AuditEvent
	Payload    json.RawMessage
}

type EvidenceReference struct {
	EventID     string               `json:"eventId"`
	Sequence    uint64               `json:"sequence"`
	ObservedUTC time.Time            `json:"observedUtc"`
	Category    domain.EventCategory `json:"category"`
	Action      string               `json:"action"`
	ObjectKey   string               `json:"objectKey,omitempty"`
}

func EvidenceFromEvent(event domain.AuditEvent) EvidenceReference {
	return EvidenceReference{
		EventID: event.EventID, Sequence: event.Sequence, ObservedUTC: event.ObservedUTC,
		Category: event.Category, Action: event.Action, ObjectKey: event.ObjectKey,
	}
}

type Finding struct {
	ID               string              `json:"id"`
	RuleID           string              `json:"ruleId"`
	Key              string              `json:"key"`
	SessionID        string              `json:"sessionId"`
	Title            string              `json:"title"`
	Summary          string              `json:"summary"`
	Level            Level               `json:"level"`
	Score            uint8               `json:"score"`
	Confidence       float64             `json:"confidence"`
	Status           FindingStatus       `json:"status"`
	FirstObservedUTC time.Time           `json:"firstObservedUtc"`
	LastObservedUTC  time.Time           `json:"lastObservedUtc"`
	Evidence         []EvidenceReference `json:"evidence"`
	Tags             []string            `json:"tags,omitempty"`
}

func (finding Finding) Validate() error {
	if strings.TrimSpace(finding.RuleID) == "" || strings.TrimSpace(finding.Key) == "" ||
		strings.TrimSpace(finding.SessionID) == "" || strings.TrimSpace(finding.Title) == "" ||
		strings.TrimSpace(finding.Summary) == "" || !finding.Level.valid() ||
		finding.Score > 100 || finding.Confidence < 0 || finding.Confidence > 1 ||
		(finding.Status != "" && !finding.Status.valid()) ||
		!isUTC(finding.FirstObservedUTC) || !isUTC(finding.LastObservedUTC) ||
		finding.LastObservedUTC.Before(finding.FirstObservedUTC) || len(finding.Evidence) == 0 {
		return ErrFindingInvalid
	}
	for _, evidence := range finding.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (status FindingStatus) valid() bool {
	return status == FindingStatusKnown || status == FindingStatusPendingReview || status == FindingStatusActionNeeded
}

func (evidence EvidenceReference) Validate() error {
	if strings.TrimSpace(evidence.EventID) == "" || evidence.Sequence == 0 ||
		strings.TrimSpace(evidence.Action) == "" || !isUTC(evidence.ObservedUTC) {
		return ErrEvidenceInvalid
	}
	return nil
}

type Rule interface {
	ID() string
	Evaluate(ctx context.Context, events []Event) ([]Finding, error)
}

type VersionedRule interface {
	Rule
	Version() string
}

type RuleVersion struct {
	RuleID  string `json:"ruleId"`
	Version string `json:"version"`
}

type RuleFailure struct {
	RuleID  string `json:"ruleId"`
	Message string `json:"message"`
}

type Evaluation struct {
	EventCount       uint64        `json:"eventCount"`
	CoverageGapCount uint64        `json:"coverageGapCount"`
	SessionID        string        `json:"sessionId"`
	Findings         []Finding     `json:"findings"`
	Failures         []RuleFailure `json:"failures,omitempty"`
	RuleVersions     []RuleVersion `json:"ruleVersions,omitempty"`
}

func (level Level) valid() bool {
	switch level {
	case LevelInformational, LevelLow, LevelMedium, LevelHigh, LevelCritical:
		return true
	default:
		return false
	}
}

func isUTC(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
