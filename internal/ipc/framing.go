package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"desktopguardpro/internal/contracts"
)

const MaxMessageSize = 1024 * 1024

var (
	ErrEmptyMessage     = errors.New("message frame is empty")
	ErrMessageTooLarge  = errors.New("message frame exceeds size limit")
	ErrMalformedMessage = errors.New("message frame is malformed")
)

func WriteMessage(writer io.Writer, message contracts.Message) error {
	if err := message.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if len(payload) == 0 {
		return ErrEmptyMessage
	}
	if len(payload) > MaxMessageSize {
		return ErrMessageTooLarge
	}

	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	if err := writeFull(writer, header); err != nil {
		return fmt.Errorf("write message header: %w", err)
	}
	if err := writeFull(writer, payload); err != nil {
		return fmt.Errorf("write message payload: %w", err)
	}
	return nil
}

func ReadMessage(reader io.Reader) (contracts.Message, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return contracts.Message{}, fmt.Errorf("read message header: %w", err)
	}

	size := binary.LittleEndian.Uint32(header)
	if size == 0 {
		return contracts.Message{}, ErrEmptyMessage
	}
	if size > MaxMessageSize {
		return contracts.Message{}, ErrMessageTooLarge
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return contracts.Message{}, fmt.Errorf("read message payload: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	var message contracts.Message
	if err := decoder.Decode(&message); err != nil {
		return contracts.Message{}, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return contracts.Message{}, err
	}
	if err := message.Validate(); err != nil {
		return contracts.Message{}, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	return message, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrMalformedMessage)
		}
		return fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	return nil
}

func writeFull(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
