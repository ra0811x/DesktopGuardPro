package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrEventIDRequired          = errors.New("event id is required")
	ErrEventSessionIDRequired   = errors.New("event session id is required")
	ErrEventSequenceRequired    = errors.New("event sequence must be greater than zero")
	ErrEventActionRequired      = errors.New("event action is required")
	ErrEventObservedUTCRequired = errors.New("event observed time must be a non-zero UTC value")
	ErrEventSourceRequired      = errors.New("event source is required")
	ErrInvalidEventCategory     = errors.New("invalid event category")
	ErrInvalidEventSeverity     = errors.New("invalid event severity")
	ErrInvalidEventConfidence   = errors.New("invalid event confidence")
)

type EventCategory string

const (
	EventCategoryFile     EventCategory = "file"
	EventCategoryProcess  EventCategory = "process"
	EventCategorySoftware EventCategory = "software"
	EventCategorySystem   EventCategory = "system"
	EventCategoryDevice   EventCategory = "device"
	EventCategoryHealth   EventCategory = "health"
)

type EventSeverity string

const (
	EventSeverityLow    EventSeverity = "low"
	EventSeverityMedium EventSeverity = "medium"
	EventSeverityHigh   EventSeverity = "high"
)

type EventConfidence string

const (
	EventConfidenceDirect       EventConfidence = "direct"
	EventConfidenceCorrelated   EventConfidence = "correlated"
	EventConfidenceSnapshotDiff EventConfidence = "snapshot_diff"
)

type AuditEvent struct {
	EventID          string          `json:"eventId"`
	SessionID        string          `json:"sessionId"`
	Sequence         uint64          `json:"sequence"`
	Category         EventCategory   `json:"category"`
	Action           string          `json:"action"`
	Severity         EventSeverity   `json:"severity"`
	ObservedUTC      time.Time       `json:"observedUtc"`
	MonotonicTicks   int64           `json:"monotonicTicks"`
	WindowsSessionID *uint32         `json:"windowsSessionId,omitempty"`
	UserSIDHash      []byte          `json:"userSidHash,omitempty"`
	ProcessKey       string          `json:"processKey,omitempty"`
	ObjectKey        string          `json:"objectKey,omitempty"`
	Source           string          `json:"source"`
	Confidence       EventConfidence `json:"confidence"`
	EncryptedPayload []byte          `json:"encryptedPayload,omitempty"`
	PreviousHash     []byte          `json:"previousHash,omitempty"`
	EventHash        []byte          `json:"eventHash,omitempty"`
}

func (event AuditEvent) Validate() error {
	if strings.TrimSpace(event.EventID) == "" {
		return ErrEventIDRequired
	}
	if strings.TrimSpace(event.SessionID) == "" {
		return ErrEventSessionIDRequired
	}
	if event.Sequence == 0 {
		return ErrEventSequenceRequired
	}
	if !event.Category.valid() {
		return ErrInvalidEventCategory
	}
	if strings.TrimSpace(event.Action) == "" {
		return ErrEventActionRequired
	}
	if !event.Severity.valid() {
		return ErrInvalidEventSeverity
	}
	if event.ObservedUTC.IsZero() {
		return ErrEventObservedUTCRequired
	}
	_, offset := event.ObservedUTC.Zone()
	if offset != 0 {
		return ErrEventObservedUTCRequired
	}
	if strings.TrimSpace(event.Source) == "" {
		return ErrEventSourceRequired
	}
	if !event.Confidence.valid() {
		return ErrInvalidEventConfidence
	}
	return nil
}

func (category EventCategory) valid() bool {
	switch category {
	case EventCategoryFile,
		EventCategoryProcess,
		EventCategorySoftware,
		EventCategorySystem,
		EventCategoryDevice,
		EventCategoryHealth:
		return true
	default:
		return false
	}
}

func (severity EventSeverity) valid() bool {
	switch severity {
	case EventSeverityLow, EventSeverityMedium, EventSeverityHigh:
		return true
	default:
		return false
	}
}

func (confidence EventConfidence) valid() bool {
	switch confidence {
	case EventConfidenceDirect,
		EventConfidenceCorrelated,
		EventConfidenceSnapshotDiff:
		return true
	default:
		return false
	}
}
