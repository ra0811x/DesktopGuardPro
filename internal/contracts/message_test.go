package contracts

import (
	_ "embed"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

//go:embed message.schema.json
var messageSchema []byte

func TestSessionHistoryAndSecurityMessagesMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, value := range schema.Properties.Type.Enum {
		types[value] = true
	}
	for _, value := range []string{"session.list", "session.list.result", "session.end_verification.create", "session.end_verification.result", "directory_monitoring.get", "directory_monitoring.update", "directory_monitoring.result"} {
		if _, err := NewMessage("schema", MessageType(value), time.Now().UTC().Add(time.Minute), struct{}{}); err != nil {
			t.Errorf("%s: %v", value, err)
		}
		if !types[value] {
			t.Errorf("%s missing from schema", value)
		}
	}
}

func TestSessionStartPreviewMessagesMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatal(err)
	}

	schemaTypes := make(map[string]bool, len(schema.Properties.Type.Enum))
	for _, value := range schema.Properties.Type.Enum {
		schemaTypes[value] = true
	}
	for messageType, expected := range map[MessageType]string{
		MessageTypeSessionStartPreviewGet:    "session.start_preview.get",
		MessageTypeSessionStartPreviewResult: "session.start_preview.result",
	} {
		if string(messageType) != expected {
			t.Errorf("message type = %q, want %q", messageType, expected)
		}
		if _, err := NewMessage("start-preview-schema", messageType, time.Now().UTC().Add(time.Minute), struct{}{}); err != nil {
			t.Errorf("%s: %v", messageType, err)
		}
		if !schemaTypes[expected] {
			t.Errorf("%s missing from schema", expected)
		}
	}
}

func TestSessionBaselineReviewMessagesMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatal(err)
	}

	schemaTypes := make(map[string]bool, len(schema.Properties.Type.Enum))
	for _, value := range schema.Properties.Type.Enum {
		schemaTypes[value] = true
	}
	for messageType, expected := range map[MessageType]string{
		MessageTypeSessionBaselineReviewGet:     "session.baseline_review.get",
		MessageTypeSessionBaselineReviewResolve: "session.baseline_review.resolve",
		MessageTypeSessionBaselineReviewResult:  "session.baseline_review.result",
	} {
		if string(messageType) != expected {
			t.Errorf("message type = %q, want %q", messageType, expected)
		}
		if _, err := NewMessage("baseline-review-schema", messageType, time.Now().UTC().Add(time.Minute), struct{}{}); err != nil {
			t.Errorf("%s: %v", messageType, err)
		}
		if !schemaTypes[expected] {
			t.Errorf("%s missing from schema", expected)
		}
	}
}

func TestAssetDifferenceMessagesMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatal(err)
	}
	schemaTypes := make(map[string]bool, len(schema.Properties.Type.Enum))
	for _, value := range schema.Properties.Type.Enum {
		schemaTypes[value] = true
	}
	for messageType, expected := range map[MessageType]string{
		MessageTypeAssetDifferenceQuery:  "asset_difference.query",
		MessageTypeAssetDifferenceResult: "asset_difference.result",
	} {
		if string(messageType) != expected {
			t.Errorf("message type = %q, want %q", messageType, expected)
		}
		if _, err := NewMessage(
			"asset-difference-schema",
			messageType,
			time.Now().UTC().Add(time.Minute),
			struct{}{},
		); err != nil {
			t.Errorf("%s: %v", messageType, err)
		}
		if !schemaTypes[expected] {
			t.Errorf("%s missing from schema", expected)
		}
	}
}

func TestAgentActivityMessagesMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatal(err)
	}
	schemaTypes := make(map[string]bool, len(schema.Properties.Type.Enum))
	for _, value := range schema.Properties.Type.Enum {
		schemaTypes[value] = true
	}
	for messageType, expected := range map[MessageType]string{
		MessageTypeAgentActivityReport: "agent.activity.report",
		MessageTypeAgentActivityResult: "agent.activity.result",
	} {
		if string(messageType) != expected {
			t.Errorf("message type = %q, want %q", messageType, expected)
		}
		if _, err := NewMessage("agent-activity-schema", messageType, time.Now().UTC().Add(time.Minute), struct{}{}); err != nil {
			t.Errorf("%s: %v", messageType, err)
		}
		if !schemaTypes[expected] {
			t.Errorf("%s missing from schema", expected)
		}
	}
}

func TestSessionRetentionMessagesMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatal(err)
	}
	schemaTypes := make(map[string]bool, len(schema.Properties.Type.Enum))
	for _, value := range schema.Properties.Type.Enum {
		schemaTypes[value] = true
	}
	for messageType, expected := range map[MessageType]string{
		MessageTypeSessionRetentionLockUpdate:  "session.retention_lock.update",
		MessageTypeSessionRetentionLockResult:  "session.retention_lock.result",
		MessageTypeSessionRetentionPrune:       "session.retention_prune",
		MessageTypeSessionRetentionPruneResult: "session.retention_prune.result",
	} {
		if string(messageType) != expected {
			t.Errorf("message type = %q, want %q", messageType, expected)
		}
		if _, err := NewMessage("retention-schema", messageType, time.Now().UTC().Add(time.Minute), struct{}{}); err != nil {
			t.Errorf("%s: %v", messageType, err)
		}
		if !schemaTypes[expected] {
			t.Errorf("%s missing from schema", expected)
		}
	}
}

func TestNewMessage(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	message, err := NewMessage(
		"request-1",
		MessageTypeHealthGet,
		deadline,
		map[string]string{"scope": "service"},
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	if message.Version != ProtocolVersion {
		t.Fatalf("message.Version = %d, want %d", message.Version, ProtocolVersion)
	}
	if message.DeadlineUTC.Location() != time.UTC {
		t.Fatalf("message.DeadlineUTC location = %v, want UTC", message.DeadlineUTC.Location())
	}

	var payload map[string]string
	if err := message.DecodePayload(&payload); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if payload["scope"] != "service" {
		t.Fatalf("payload scope = %q, want %q", payload["scope"], "service")
	}
}

func TestMessageValidate(t *testing.T) {
	t.Parallel()

	message := validMessage()
	if err := message.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestMessageValidateAcceptsKnownTypes(t *testing.T) {
	t.Parallel()

	types := []MessageType{
		MessageTypeHealthGet,
		MessageTypeHealthResult,
		MessageTypeSessionCreate,
		MessageTypeSessionTransition,
		MessageTypeSessionCurrentGet,
		MessageTypeSessionResult,
		MessageTypeTimelineQuery,
		MessageTypeTimelineResult,
		MessageTypeRiskEvaluate,
		MessageTypeRiskResult,
		MessageTypeRiskFindingStatusUpdate,
		MessageTypeRiskFindingStatusResult,
		MessageTypeAssetDifferenceQuery,
		MessageTypeAssetDifferenceResult,
		MessageTypeAgentActivityReport,
		MessageTypeAgentActivityResult,
		MessageTypeSessionRetentionLockUpdate,
		MessageTypeSessionRetentionLockResult,
		MessageTypeSessionRetentionPrune,
		MessageTypeSessionRetentionPruneResult,
		MessageTypeReportExport,
		MessageTypeReportResult,
		MessageTypeReportChunkGet,
		MessageTypeReportChunkResult,
		MessageTypeError,
	}

	for _, messageType := range types {
		message := validMessage()
		message.Type = messageType
		if err := message.Validate(); err != nil {
			t.Fatalf("Validate() type %q error = %v", messageType, err)
		}
	}
}

func TestMessageValidateRejectsInvalidEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Message)
		wantErr error
	}{
		{name: "version", mutate: func(message *Message) { message.Version++ }, wantErr: ErrUnsupportedProtocolVersion},
		{name: "request id", mutate: func(message *Message) { message.RequestID = " " }, wantErr: ErrRequestIDRequired},
		{name: "type", mutate: func(message *Message) { message.Type = "unknown" }, wantErr: ErrInvalidMessageType},
		{name: "deadline", mutate: func(message *Message) { message.DeadlineUTC = time.Time{} }, wantErr: ErrDeadlineRequired},
		{name: "deadline zone", mutate: func(message *Message) {
			message.DeadlineUTC = time.Date(2026, time.August, 23, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
		}, wantErr: ErrDeadlineRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			message := validMessage()
			tt.mutate(&message)
			err := message.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestMessageSchemaIsValidJSON(t *testing.T) {
	t.Parallel()

	var schema map[string]any
	if err := json.Unmarshal(messageSchema, &schema); err != nil {
		t.Fatalf("message schema is invalid JSON: %v", err)
	}
	if schema["$schema"] == nil {
		t.Fatal("message schema has no $schema declaration")
	}
}

func validMessage() Message {
	return Message{
		Version:     ProtocolVersion,
		RequestID:   "request-1",
		Type:        MessageTypeHealthGet,
		DeadlineUTC: time.Date(2026, time.August, 23, 2, 0, 0, 0, time.UTC),
		Payload:     json.RawMessage(`{"scope":"service"}`),
	}
}
