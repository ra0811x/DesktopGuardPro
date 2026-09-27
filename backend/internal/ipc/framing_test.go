package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
)

func TestMessageFrameRoundTrip(t *testing.T) {
	t.Parallel()

	want, err := contracts.NewMessage(
		"request-1",
		contracts.MessageTypeHealthGet,
		time.Now().UTC().Add(time.Second),
		map[string]string{"scope": "service"},
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	var buffer bytes.Buffer
	if err := WriteMessage(&buffer, want); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	got, err := ReadMessage(&buffer)
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if got.RequestID != want.RequestID || got.Type != want.Type {
		t.Fatalf("ReadMessage() = %#v, want request %q and type %q", got, want.RequestID, want.Type)
	}
}

func TestWriteMessageRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	message, err := contracts.NewMessage(
		"request-1",
		contracts.MessageTypeHealthGet,
		time.Now().UTC().Add(time.Second),
		map[string]string{"data": strings.Repeat("x", MaxMessageSize)},
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	err = WriteMessage(&bytes.Buffer{}, message)
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("WriteMessage() error = %v, want %v", err, ErrMessageTooLarge)
	}
}

func TestReadMessageRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.LittleEndian, uint32(MaxMessageSize+1)); err != nil {
		t.Fatalf("binary.Write() error = %v", err)
	}

	_, err := ReadMessage(&buffer)
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("ReadMessage() error = %v, want %v", err, ErrMessageTooLarge)
	}
}

func TestReadMessageRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"version":1,"requestId":"request-1","type":"health.get","deadlineUtc":"2026-08-23T02:00:00Z","payload":{},"unexpected":true}`)
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.LittleEndian, uint32(len(payload))); err != nil {
		t.Fatalf("binary.Write() error = %v", err)
	}
	if _, err := buffer.Write(payload); err != nil {
		t.Fatalf("buffer.Write() error = %v", err)
	}

	_, err := ReadMessage(&buffer)
	if !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("ReadMessage() error = %v, want %v", err, ErrMalformedMessage)
	}
}

func TestReadMessageRejectsEmptyFrame(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.LittleEndian, uint32(0)); err != nil {
		t.Fatalf("binary.Write() error = %v", err)
	}

	_, err := ReadMessage(&buffer)
	if !errors.Is(err, ErrEmptyMessage) {
		t.Fatalf("ReadMessage() error = %v, want %v", err, ErrEmptyMessage)
	}
}
