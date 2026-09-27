package storage

import (
	"context"
	"encoding/base64"
	"fmt"

	"desktopguardpro/internal/domain"
)

func sensitiveKeyAAD(event domain.AuditEvent, field string) []byte {
	return []byte("Desktop Guard Pro|event-key|v1\x00" + event.SessionID + "\x00" + event.EventID + "\x00" + field)
}

func (repository *Repository) sealEventKeys(event domain.AuditEvent) (domain.AuditEvent, error) {
	for _, field := range []struct {
		name  string
		value *string
	}{{"process", &event.ProcessKey}, {"object", &event.ObjectKey}} {
		nonce, ciphertext, err := repository.cipher.Encrypt([]byte(*field.value), sensitiveKeyAAD(event, field.name))
		if err != nil {
			return domain.AuditEvent{}, err
		}
		*field.value = base64.StdEncoding.EncodeToString(append(nonce, ciphertext...))
	}
	return event, nil
}

func (repository *Repository) openEventKeys(event *domain.AuditEvent, encrypted bool) error {
	if !encrypted {
		return nil
	} // Legacy rows are migrated before the repository is exposed.
	for _, field := range []struct {
		name  string
		value *string
	}{{"process", &event.ProcessKey}, {"object", &event.ObjectKey}} {
		encoded, err := base64.StdEncoding.DecodeString(*field.value)
		nonceSize := repository.cipher.aead.NonceSize()
		if err != nil || len(encoded) < nonceSize {
			return ErrEventIntegrity
		}
		plaintext, err := repository.cipher.Decrypt(encoded[:nonceSize], encoded[nonceSize:], sensitiveKeyAAD(*event, field.name))
		if err != nil {
			return fmt.Errorf("%w: encrypted event key: %v", ErrEventIntegrity, err)
		}
		*field.value = string(plaintext)
		clear(plaintext)
	}
	return nil
}

// Encrypt only the stored representation. The logical keys, payload AAD and
// authenticated event-chain hashes remain unchanged across this migration.
func (repository *Repository) migrateSensitiveEventKeys(ctx context.Context) error {
	conn, err := repository.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT event_id, session_id, process_key, object_key FROM audit_events WHERE keys_encrypted = 0")
	if err != nil {
		return err
	}
	var events []domain.AuditEvent
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.EventID, &event.SessionID, &event.ProcessKey, &event.ObjectKey); err != nil {
			rows.Close()
			return err
		}
		event, err = repository.sealEventKeys(event)
		if err != nil {
			rows.Close()
			return err
		}
		events = append(events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, "UPDATE audit_events SET process_key = ?, object_key = ?, keys_encrypted = 1 WHERE event_id = ?", event.ProcessKey, event.ObjectKey, event.EventID); err != nil {
			return err
		}
	}
	if len(events) > 0 {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO sensitive_key_cleanup VALUES (1)"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var cleanup bool
	if err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sensitive_key_cleanup)").Scan(&cleanup); err != nil {
		return err
	}
	if !cleanup {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("compact encrypted event keys: %w", err)
	}
	var busy, frames, completed int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &completed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("encrypted event key cleanup is waiting for other database users")
	}
	_, err = conn.ExecContext(ctx, "DELETE FROM sensitive_key_cleanup")
	return err
}
