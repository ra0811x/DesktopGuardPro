package storage

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"desktopguardpro/internal/domain"
)

const eventHashSize = sha256.Size

var (
	ErrInvalidEventHash = errors.New("invalid event hash")
	ErrEventIntegrity   = errors.New("event integrity verification failed")
	ErrChainCheckpoint  = errors.New("event chain checkpoint is invalid")
)

type EventIntegrity struct {
	key [sha256.Size]byte
}

func NewEventIntegrity(masterKey []byte) (*EventIntegrity, error) {
	if len(masterKey) != payloadKeySize {
		return nil, ErrInvalidPayloadKey
	}

	derivation := hmac.New(sha256.New, masterKey)
	_, _ = derivation.Write([]byte("Desktop Guard Pro|event-integrity|v1"))
	derivedKey := derivation.Sum(nil)
	integrity := &EventIntegrity{}
	copy(integrity.key[:], derivedKey)
	clear(derivedKey)
	return integrity, nil
}

func (integrity *EventIntegrity) AssociatedData(event domain.AuditEvent) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}

	var encoded bytes.Buffer
	writeField(&encoded, []byte("Desktop Guard Pro|audit-event|v1"))
	writeField(&encoded, []byte(event.EventID))
	writeField(&encoded, []byte(event.SessionID))
	writeUint64(&encoded, event.Sequence)
	writeField(&encoded, []byte(event.Category))
	writeField(&encoded, []byte(event.Action))
	writeField(&encoded, []byte(event.Severity))
	writeField(&encoded, []byte(event.ObservedUTC.UTC().Format("2006-01-02T15:04:05.999999999Z")))
	writeUint64(&encoded, uint64(event.MonotonicTicks))
	if event.WindowsSessionID == nil {
		encoded.WriteByte(0)
	} else {
		encoded.WriteByte(1)
		writeUint32(&encoded, *event.WindowsSessionID)
	}
	writeField(&encoded, event.UserSIDHash)
	writeField(&encoded, []byte(event.ProcessKey))
	writeField(&encoded, []byte(event.ObjectKey))
	writeField(&encoded, []byte(event.Source))
	writeField(&encoded, []byte(event.Confidence))
	return encoded.Bytes(), nil
}

func (integrity *EventIntegrity) Compute(event domain.AuditEvent, nonce []byte) ([]byte, error) {
	associatedData, err := integrity.AssociatedData(event)
	if err != nil {
		return nil, err
	}

	digest := hmac.New(sha256.New, integrity.key[:])
	writeField(digest, associatedData)
	writeField(digest, nonce)
	writeField(digest, event.EncryptedPayload)
	writeField(digest, event.PreviousHash)
	return digest.Sum(nil), nil
}

func (integrity *EventIntegrity) Verify(event domain.AuditEvent, nonce []byte) error {
	if len(event.EventHash) != eventHashSize {
		return ErrInvalidEventHash
	}
	expected, err := integrity.Compute(event, nonce)
	if err != nil {
		return err
	}
	if !hmac.Equal(event.EventHash, expected) {
		return ErrEventIntegrity
	}
	return nil
}

func (integrity *EventIntegrity) ComputeChainCheckpoint(sessionID string, eventCount uint64, tailHash []byte) ([]byte, error) {
	if strings.TrimSpace(sessionID) == "" || len(tailHash) != eventHashSize {
		return nil, ErrChainCheckpoint
	}

	digest := hmac.New(sha256.New, integrity.key[:])
	writeField(digest, []byte("Desktop Guard Pro|event-chain-checkpoint|v1"))
	writeField(digest, []byte(sessionID))
	writeUint64(digest, eventCount)
	writeField(digest, tailHash)
	return digest.Sum(nil), nil
}

func (integrity *EventIntegrity) VerifyChainCheckpoint(sessionID string, eventCount uint64, tailHash, commitment []byte) error {
	if len(commitment) != eventHashSize {
		return ErrChainCheckpoint
	}
	expected, err := integrity.ComputeChainCheckpoint(sessionID, eventCount, tailHash)
	if err != nil {
		return err
	}
	if !hmac.Equal(commitment, expected) {
		return ErrEventIntegrity
	}
	return nil
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeField(writer byteWriter, value []byte) {
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}

func writeUint32(writer byteWriter, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func writeUint64(writer byteWriter, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func (integrity *EventIntegrity) String() string {
	return fmt.Sprintf("EventIntegrity(HMAC-SHA256/%d)", eventHashSize*8)
}
