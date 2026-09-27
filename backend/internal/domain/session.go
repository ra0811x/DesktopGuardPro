package domain

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrSessionIDRequired           = errors.New("session id is required")
	ErrSessionNameRequired         = errors.New("session name is required")
	ErrInvalidSessionTransition    = errors.New("invalid session state transition")
	ErrInvalidMonitoringLevel      = errors.New("invalid monitoring level")
	ErrInvalidMonitoredTargetCount = errors.New("invalid monitored target count")
)

type MonitoringLevel string

const (
	MonitoringLevelStandard MonitoringLevel = "standard"
	MonitoringLevelStrict   MonitoringLevel = "strict"
)

type MonitoringImpactLevel string

const (
	MonitoringImpactLow    MonitoringImpactLevel = "low"
	MonitoringImpactMedium MonitoringImpactLevel = "medium"
	MonitoringImpactHigh   MonitoringImpactLevel = "high"
)

type MonitoringImpact struct {
	RequiresAdministrator bool
	ExpectedEventVolume   MonitoringImpactLevel
	PerformanceImpact     MonitoringImpactLevel
	StorageImpact         MonitoringImpactLevel
}

type MonitoringProfile struct {
	Level MonitoringLevel
}

type SessionStartPreview struct {
	MonitoringLevel      MonitoringLevel
	MonitoredTargetCount int
	Impact               MonitoringImpact
}

func DefaultMonitoringProfile() MonitoringProfile {
	return MonitoringProfile{Level: MonitoringLevelStandard}
}

func (p MonitoringProfile) Validate() error {
	switch p.Level {
	case MonitoringLevelStandard, MonitoringLevelStrict:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidMonitoringLevel, p.Level)
	}
}

func (p MonitoringProfile) StartPreview(monitoredTargetCount int) (SessionStartPreview, error) {
	if err := p.Validate(); err != nil {
		return SessionStartPreview{}, err
	}
	if monitoredTargetCount < 0 {
		return SessionStartPreview{}, fmt.Errorf("%w: %d", ErrInvalidMonitoredTargetCount, monitoredTargetCount)
	}

	return SessionStartPreview{
		MonitoringLevel:      p.Level,
		MonitoredTargetCount: monitoredTargetCount,
		Impact:               monitoringImpactForLevel(p.Level),
	}, nil
}

func monitoringImpactForLevel(level MonitoringLevel) MonitoringImpact {
	if level == MonitoringLevelStrict {
		return MonitoringImpact{
			RequiresAdministrator: true,
			ExpectedEventVolume:   MonitoringImpactHigh,
			PerformanceImpact:     MonitoringImpactHigh,
			StorageImpact:         MonitoringImpactHigh,
		}
	}

	return MonitoringImpact{
		ExpectedEventVolume: MonitoringImpactMedium,
		PerformanceImpact:   MonitoringImpactLow,
		StorageImpact:       MonitoringImpactMedium,
	}
}

type SessionState string

const (
	SessionStateDraft          SessionState = "draft"
	SessionStatePreparing      SessionState = "preparing"
	SessionStateBaselineReview SessionState = "baseline_review"
	SessionStateActive         SessionState = "active"
	SessionStateDegraded       SessionState = "degraded"
	SessionStatePaused         SessionState = "paused"
	SessionStateFinalizing     SessionState = "finalizing"
	SessionStateCompleted      SessionState = "completed"
	SessionStateFailed         SessionState = "failed"
)

type Session struct {
	ID               string
	Name             string
	State            SessionState
	Revision         uint64
	MonitoringLevel  MonitoringLevel
	MonitoringPolicy MonitoringPolicy
}

func NewSession(id, name string) (*Session, error) {
	return NewSessionWithMonitoringLevel(id, name, DefaultMonitoringProfile().Level)
}

func NewSessionWithMonitoringLevel(id, name string, level MonitoringLevel) (*Session, error) {
	if err := (MonitoringProfile{Level: level}).Validate(); err != nil {
		return nil, err
	}
	policy := DefaultMonitoringPolicy()
	policy.StrictReadAuditEnabled = level == MonitoringLevelStrict
	return NewSessionWithMonitoringPolicy(id, name, policy)
}

func NewSessionWithMonitoringPolicy(id, name string, policy MonitoringPolicy) (*Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrSessionIDRequired
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrSessionNameRequired
	}
	policy = policy.Resolved()
	if err := policy.Validate(); err != nil {
		return nil, err
	}

	return &Session{
		ID:               id,
		Name:             name,
		State:            SessionStateDraft,
		MonitoringLevel:  policy.MonitoringLevel(),
		MonitoringPolicy: policy,
	}, nil
}

func (s *Session) Transition(next SessionState) error {
	if !canTransition(s.State, next) {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidSessionTransition,
			s.State,
			next,
		)
	}

	s.State = next
	s.Revision++
	return nil
}

func canTransition(current, next SessionState) bool {
	switch current {
	case SessionStateDraft:
		return next == SessionStatePreparing
	case SessionStatePreparing:
		return next == SessionStateBaselineReview || next == SessionStateActive || next == SessionStateFailed
	case SessionStateBaselineReview:
		return next == SessionStateActive || next == SessionStateFailed
	case SessionStateActive:
		return next == SessionStateDegraded || next == SessionStatePaused || next == SessionStateFinalizing
	case SessionStateDegraded:
		return next == SessionStateActive || next == SessionStatePaused || next == SessionStateFinalizing
	case SessionStatePaused:
		return next == SessionStateActive || next == SessionStateFinalizing
	case SessionStateFinalizing:
		return next == SessionStateCompleted
	case SessionStateCompleted, SessionStateFailed:
		return false
	default:
		return false
	}
}
