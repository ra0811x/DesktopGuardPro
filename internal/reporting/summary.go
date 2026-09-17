package reporting

import (
	"errors"
	"sort"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/risk"
)

var ErrSummaryInputInvalid = errors.New("session summary input is invalid")

type HealthStatus string

const (
	HealthStatusHealthy  HealthStatus = "healthy"
	HealthStatusDegraded HealthStatus = "degraded"
)

type CoverageGap struct {
	EventID     string    `json:"eventId"`
	Source      string    `json:"source"`
	Action      string    `json:"action"`
	ObservedUTC time.Time `json:"observedUtc"`
}

type SessionSummary struct {
	SessionID         string                          `json:"sessionId"`
	SessionName       string                          `json:"sessionName"`
	SessionState      domain.SessionState             `json:"sessionState"`
	EventCount        uint64                          `json:"eventCount"`
	FirstObservedUTC  *time.Time                      `json:"firstObservedUtc,omitempty"`
	LastObservedUTC   *time.Time                      `json:"lastObservedUtc,omitempty"`
	EventsByCategory  map[domain.EventCategory]uint64 `json:"eventsByCategory"`
	FindingCount      uint64                          `json:"findingCount"`
	FindingsByLevel   map[risk.Level]uint64           `json:"findingsByLevel"`
	HighestRisk       risk.Level                      `json:"highestRisk,omitempty"`
	CollectorHealth   HealthStatus                    `json:"collectorHealth"`
	CoverageGaps      []CoverageGap                   `json:"coverageGaps,omitempty"`
	RuleFailureCount  uint64                          `json:"ruleFailureCount"`
	IntegrityVerified bool                            `json:"integrityVerified"`
}

func BuildSessionSummary(
	session domain.Session,
	events []domain.AuditEvent,
	evaluation risk.Evaluation,
	integrityVerified bool,
) (SessionSummary, error) {
	if err := validateSummarySession(session); err != nil || evaluation.SessionID != session.ID {
		return SessionSummary{}, ErrSummaryInputInvalid
	}
	summary := SessionSummary{
		SessionID: session.ID, SessionName: session.Name, SessionState: session.State,
		EventsByCategory: make(map[domain.EventCategory]uint64),
		FindingsByLevel:  make(map[risk.Level]uint64),
		CollectorHealth:  HealthStatusHealthy, IntegrityVerified: integrityVerified,
		RuleFailureCount: uint64(len(evaluation.Failures)),
	}
	seenEvents := make(map[string]struct{}, len(events))
	for _, event := range events {
		if err := event.Validate(); err != nil || event.SessionID != session.ID {
			return SessionSummary{}, ErrSummaryInputInvalid
		}
		if _, exists := seenEvents[event.EventID]; exists {
			return SessionSummary{}, ErrSummaryInputInvalid
		}
		seenEvents[event.EventID] = struct{}{}
		summary.EventCount++
		summary.EventsByCategory[event.Category]++
		updateObservedRange(&summary, event.ObservedUTC)
		if event.Category == domain.EventCategoryHealth && isCoverageGapAction(event.Action) {
			summary.CoverageGaps = append(summary.CoverageGaps, CoverageGap{
				EventID: event.EventID, Source: event.Source, Action: event.Action, ObservedUTC: event.ObservedUTC,
			})
		}
	}
	for _, finding := range evaluation.Findings {
		if err := finding.Validate(); err != nil || finding.SessionID != session.ID {
			return SessionSummary{}, ErrSummaryInputInvalid
		}
		summary.FindingCount++
		summary.FindingsByLevel[finding.Level]++
		if riskRank(finding.Level) > riskRank(summary.HighestRisk) {
			summary.HighestRisk = finding.Level
		}
	}
	if len(summary.CoverageGaps) > 0 || summary.RuleFailureCount > 0 || !integrityVerified ||
		session.State == domain.SessionStateDegraded || session.State == domain.SessionStateFailed {
		summary.CollectorHealth = HealthStatusDegraded
	}
	sort.Slice(summary.CoverageGaps, func(left, right int) bool {
		if !summary.CoverageGaps[left].ObservedUTC.Equal(summary.CoverageGaps[right].ObservedUTC) {
			return summary.CoverageGaps[left].ObservedUTC.Before(summary.CoverageGaps[right].ObservedUTC)
		}
		return summary.CoverageGaps[left].EventID < summary.CoverageGaps[right].EventID
	})
	return summary, nil
}

func validateSummarySession(session domain.Session) error {
	validated, err := domain.NewSession(session.ID, session.Name)
	if err != nil || validated.ID != session.ID || validated.Name != session.Name {
		return ErrSummaryInputInvalid
	}
	switch session.State {
	case domain.SessionStateDraft, domain.SessionStatePreparing, domain.SessionStateActive,
		domain.SessionStatePaused, domain.SessionStateDegraded, domain.SessionStateFinalizing,
		domain.SessionStateCompleted, domain.SessionStateFailed:
		return nil
	default:
		return ErrSummaryInputInvalid
	}
}

func updateObservedRange(summary *SessionSummary, observed time.Time) {
	if summary.FirstObservedUTC == nil || observed.Before(*summary.FirstObservedUTC) {
		value := observed
		summary.FirstObservedUTC = &value
	}
	if summary.LastObservedUTC == nil || observed.After(*summary.LastObservedUTC) {
		value := observed
		summary.LastObservedUTC = &value
	}
}

func isCoverageGapAction(action string) bool {
	action = strings.TrimSpace(action)
	return action == "observation_queue_overflow" || action == "directory_snapshot_required" ||
		action == "service_recovered_after_interruption" || action == "service_recovered_after_sleep" || action == "usn_journal_gap_detected" ||
		action == "file_hash_queue_overflow" || action == "strict_read_audit_unavailable" ||
		action == "system_asset_snapshot_unavailable" || action == "security_log_monitor_unavailable" ||
		action == "process_snapshot_unavailable" || action == "software_repair_monitor_unavailable"
}

func riskRank(level risk.Level) int {
	switch level {
	case risk.LevelCritical:
		return 5
	case risk.LevelHigh:
		return 4
	case risk.LevelMedium:
		return 3
	case risk.LevelLow:
		return 2
	case risk.LevelInformational:
		return 1
	default:
		return 0
	}
}
