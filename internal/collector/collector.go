package collector

import (
	"context"
	"errors"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

var (
	ErrCollectorNameRequired = errors.New("collector name is required")
	ErrObservationInvalid    = errors.New("collector observation is invalid")
)

type Observation struct {
	Category         domain.EventCategory
	Action           string
	Severity         domain.EventSeverity
	ObservedUTC      time.Time
	MonotonicTicks   int64
	WindowsSessionID *uint32
	UserSIDHash      []byte
	ProcessKey       string
	ObjectKey        string
	Source           string
	Confidence       domain.EventConfidence
	Payload          []byte
}

func (observation Observation) Validate() error {
	if strings.TrimSpace(observation.Action) == "" ||
		strings.TrimSpace(observation.Source) == "" ||
		observation.ObservedUTC.IsZero() {
		return ErrObservationInvalid
	}
	_, offset := observation.ObservedUTC.Zone()
	if offset != 0 || !knownCategory(observation.Category) ||
		!knownSeverity(observation.Severity) ||
		!knownConfidence(observation.Confidence) {
		return ErrObservationInvalid
	}
	return nil
}

type Sink interface {
	Emit(ctx context.Context, observation Observation) error
}

type Collector interface {
	Name() string
	Run(ctx context.Context, sink Sink) error
}

func knownCategory(category domain.EventCategory) bool {
	switch category {
	case domain.EventCategoryFile, domain.EventCategoryProcess,
		domain.EventCategorySoftware, domain.EventCategorySystem,
		domain.EventCategoryDevice, domain.EventCategoryHealth:
		return true
	default:
		return false
	}
}

func knownSeverity(severity domain.EventSeverity) bool {
	switch severity {
	case domain.EventSeverityLow, domain.EventSeverityMedium, domain.EventSeverityHigh:
		return true
	default:
		return false
	}
}

func knownConfidence(confidence domain.EventConfidence) bool {
	switch confidence {
	case domain.EventConfidenceDirect, domain.EventConfidenceCorrelated,
		domain.EventConfidenceSnapshotDiff:
		return true
	default:
		return false
	}
}
