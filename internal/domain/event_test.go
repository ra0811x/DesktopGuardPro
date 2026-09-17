package domain

import (
	"errors"
	"testing"
	"time"
)

func TestAuditEventValidate(t *testing.T) {
	t.Parallel()

	event := validAuditEvent()
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAuditEventValidateRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*AuditEvent)
		wantErr error
	}{
		{name: "event id", mutate: func(event *AuditEvent) { event.EventID = " " }, wantErr: ErrEventIDRequired},
		{name: "session id", mutate: func(event *AuditEvent) { event.SessionID = " " }, wantErr: ErrEventSessionIDRequired},
		{name: "sequence", mutate: func(event *AuditEvent) { event.Sequence = 0 }, wantErr: ErrEventSequenceRequired},
		{name: "action", mutate: func(event *AuditEvent) { event.Action = " " }, wantErr: ErrEventActionRequired},
		{name: "observed time", mutate: func(event *AuditEvent) { event.ObservedUTC = time.Time{} }, wantErr: ErrEventObservedUTCRequired},
		{name: "source", mutate: func(event *AuditEvent) { event.Source = " " }, wantErr: ErrEventSourceRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := validAuditEvent()
			tt.mutate(&event)

			err := event.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuditEventValidateEnumerations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*AuditEvent)
		wantErr error
	}{
		{name: "category", mutate: func(event *AuditEvent) { event.Category = "unknown" }, wantErr: ErrInvalidEventCategory},
		{name: "severity", mutate: func(event *AuditEvent) { event.Severity = "critical" }, wantErr: ErrInvalidEventSeverity},
		{name: "confidence", mutate: func(event *AuditEvent) { event.Confidence = "guessed" }, wantErr: ErrInvalidEventConfidence},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := validAuditEvent()
			tt.mutate(&event)

			err := event.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuditEventRequiresUTC(t *testing.T) {
	t.Parallel()

	event := validAuditEvent()
	event.ObservedUTC = time.Date(2026, time.August, 23, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))

	err := event.Validate()
	if !errors.Is(err, ErrEventObservedUTCRequired) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrEventObservedUTCRequired)
	}
}

func validAuditEvent() AuditEvent {
	return AuditEvent{
		EventID:        "event-1",
		SessionID:      "session-1",
		Sequence:       1,
		Category:       EventCategoryHealth,
		Action:         "session.started",
		Severity:       EventSeverityLow,
		ObservedUTC:    time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC),
		MonotonicTicks: 1,
		Source:         "session-coordinator/v1",
		Confidence:     EventConfidenceDirect,
	}
}
