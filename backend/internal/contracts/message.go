package contracts

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const ProtocolVersion uint16 = 1

var (
	ErrUnsupportedProtocolVersion = errors.New("unsupported protocol version")
	ErrRequestIDRequired          = errors.New("request id is required")
	ErrInvalidMessageType         = errors.New("invalid message type")
	ErrDeadlineRequired           = errors.New("deadline must be a non-zero UTC value")
)

type MessageType string

const (
	MessageTypeHealthGet                    MessageType = "health.get"
	MessageTypeHealthResult                 MessageType = "health.result"
	MessageTypeDirectoryMonitoringGet       MessageType = "directory_monitoring.get"
	MessageTypeDirectoryMonitoringUpdate    MessageType = "directory_monitoring.update"
	MessageTypeDirectoryMonitoringResult    MessageType = "directory_monitoring.result"
	MessageTypeSessionCreate                MessageType = "session.create"
	MessageTypeSessionTransition            MessageType = "session.transition"
	MessageTypeSessionEndVerificationCreate MessageType = "session.end_verification.create"
	MessageTypeSessionEndVerificationResult MessageType = "session.end_verification.result"
	MessageTypeSessionStartPreviewGet       MessageType = "session.start_preview.get"
	MessageTypeSessionStartPreviewResult    MessageType = "session.start_preview.result"
	MessageTypeSessionBaselineReviewGet     MessageType = "session.baseline_review.get"
	MessageTypeSessionBaselineReviewResolve MessageType = "session.baseline_review.resolve"
	MessageTypeSessionBaselineReviewResult  MessageType = "session.baseline_review.result"
	MessageTypeSessionCurrentGet            MessageType = "session.current.get"
	MessageTypeSessionList                  MessageType = "session.list"
	MessageTypeSessionListResult            MessageType = "session.list.result"
	MessageTypeSessionRetentionLockUpdate   MessageType = "session.retention_lock.update"
	MessageTypeSessionRetentionLockResult   MessageType = "session.retention_lock.result"
	MessageTypeSessionRetentionPrune        MessageType = "session.retention_prune"
	MessageTypeSessionRetentionPruneResult  MessageType = "session.retention_prune.result"
	MessageTypeSessionResult                MessageType = "session.result"
	MessageTypeTimelineQuery                MessageType = "timeline.query"
	MessageTypeTimelineResult               MessageType = "timeline.result"
	MessageTypeRiskEvaluate                 MessageType = "risk.evaluate"
	MessageTypeRiskResult                   MessageType = "risk.result"
	MessageTypeRiskFindingStatusUpdate      MessageType = "risk.finding_status.update"
	MessageTypeRiskFindingStatusResult      MessageType = "risk.finding_status.result"
	MessageTypeAssetDifferenceQuery         MessageType = "asset_difference.query"
	MessageTypeAssetDifferenceResult        MessageType = "asset_difference.result"
	MessageTypeAgentActivityReport          MessageType = "agent.activity.report"
	MessageTypeAgentActivityResult          MessageType = "agent.activity.result"
	MessageTypeInputShieldCredentialGet     MessageType = "input_shield.credential.get"
	MessageTypeInputShieldCredentialUpdate  MessageType = "input_shield.credential.update"
	MessageTypeInputShieldCredentialDelete  MessageType = "input_shield.credential.delete"
	MessageTypeInputShieldCredentialVerify  MessageType = "input_shield.credential.verify"
	MessageTypeInputShieldCredentialResult  MessageType = "input_shield.credential.result"
	MessageTypeAgentInputShieldReport       MessageType = "agent.input_shield.report"
	MessageTypeAgentInputShieldResult       MessageType = "agent.input_shield.result"
	MessageTypeInputShieldStatusGet         MessageType = "input_shield.status.get"
	MessageTypeInputShieldStatusResult      MessageType = "input_shield.status.result"
	MessageTypeInputControlGet              MessageType = "input_control.get"
	MessageTypeInputControlStart            MessageType = "input_control.start"
	MessageTypeInputControlStop             MessageType = "input_control.stop"
	MessageTypeInputControlResult           MessageType = "input_control.result"
	MessageTypeReportExport                 MessageType = "report.export"
	MessageTypeReportResult                 MessageType = "report.result"
	MessageTypeReportChunkGet               MessageType = "report.chunk.get"
	MessageTypeReportChunkResult            MessageType = "report.chunk.result"
	MessageTypeError                        MessageType = "error"
)

type Message struct {
	Version     uint16          `json:"version"`
	RequestID   string          `json:"requestId"`
	Type        MessageType     `json:"type"`
	DeadlineUTC time.Time       `json:"deadlineUtc"`
	Payload     json.RawMessage `json:"payload"`
}

func NewMessage(
	requestID string,
	messageType MessageType,
	deadline time.Time,
	payload any,
) (Message, error) {
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}

	message := Message{
		Version:     ProtocolVersion,
		RequestID:   strings.TrimSpace(requestID),
		Type:        messageType,
		DeadlineUTC: deadline.UTC(),
		Payload:     encodedPayload,
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (message Message) Validate() error {
	if message.Version != ProtocolVersion {
		return ErrUnsupportedProtocolVersion
	}
	if strings.TrimSpace(message.RequestID) == "" {
		return ErrRequestIDRequired
	}
	if !message.Type.valid() {
		return ErrInvalidMessageType
	}
	if message.DeadlineUTC.IsZero() {
		return ErrDeadlineRequired
	}
	_, offset := message.DeadlineUTC.Zone()
	if offset != 0 {
		return ErrDeadlineRequired
	}
	return nil
}

func (message Message) DecodePayload(target any) error {
	return json.Unmarshal(message.Payload, target)
}

func (messageType MessageType) valid() bool {
	switch messageType {
	case MessageTypeHealthGet,
		MessageTypeHealthResult,
		MessageTypeDirectoryMonitoringGet,
		MessageTypeDirectoryMonitoringUpdate,
		MessageTypeDirectoryMonitoringResult,
		MessageTypeSessionCreate,
		MessageTypeSessionTransition,
		MessageTypeSessionEndVerificationCreate,
		MessageTypeSessionEndVerificationResult,
		MessageTypeSessionStartPreviewGet,
		MessageTypeSessionStartPreviewResult,
		MessageTypeSessionBaselineReviewGet,
		MessageTypeSessionBaselineReviewResolve,
		MessageTypeSessionBaselineReviewResult,
		MessageTypeSessionCurrentGet,
		MessageTypeSessionList,
		MessageTypeSessionListResult,
		MessageTypeSessionRetentionLockUpdate,
		MessageTypeSessionRetentionLockResult,
		MessageTypeSessionRetentionPrune,
		MessageTypeSessionRetentionPruneResult,
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
		MessageTypeInputShieldCredentialGet,
		MessageTypeInputShieldCredentialUpdate,
		MessageTypeInputShieldCredentialDelete,
		MessageTypeInputShieldCredentialVerify,
		MessageTypeInputShieldCredentialResult,
		MessageTypeAgentInputShieldReport,
		MessageTypeAgentInputShieldResult,
		MessageTypeInputShieldStatusGet,
		MessageTypeInputShieldStatusResult,
		MessageTypeInputControlGet,
		MessageTypeInputControlStart,
		MessageTypeInputControlStop,
		MessageTypeInputControlResult,
		MessageTypeReportExport,
		MessageTypeReportResult,
		MessageTypeReportChunkGet,
		MessageTypeReportChunkResult,
		MessageTypeError:
		return true
	default:
		return false
	}
}
