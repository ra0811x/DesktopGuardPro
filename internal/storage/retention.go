package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrRetentionCutoffRequired = errors.New("retention cutoff is required")
	ErrRetentionLimitInvalid   = errors.New("retention deletion limit is invalid")
)

const defaultRetentionPruneLimit = 100

func (repository *Repository) SetSessionRetentionLock(
	ctx context.Context,
	sessionID string,
	locked bool,
) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retention lock transaction: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", sessionID).Scan(&exists); err != nil {
		return fmt.Errorf("check retention lock session: %w", err)
	}
	if exists != 1 {
		return ErrSessionNotFound
	}
	if locked {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_retention_locks (session_id, locked_utc)
			VALUES (?, ?)
			ON CONFLICT (session_id) DO UPDATE SET locked_utc = excluded.locked_utc
		`, sessionID, formatTimestamp(time.Now())); err != nil {
			return fmt.Errorf("store session retention lock: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, "DELETE FROM session_retention_locks WHERE session_id = ?", sessionID); err != nil {
		return fmt.Errorf("remove session retention lock: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retention lock transaction: %w", err)
	}
	return nil
}

func (repository *Repository) PruneTerminalSessions(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]string, error) {
	if before.IsZero() {
		return nil, ErrRetentionCutoffRequired
	}
	if limit == 0 {
		limit = defaultRetentionPruneLimit
	}
	if limit < 0 {
		return nil, ErrRetentionLimitInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin session retention transaction: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT sessions.id
		FROM sessions
		LEFT JOIN session_retention_locks locks ON locks.session_id = sessions.id
		WHERE sessions.state IN ('completed', 'failed')
		  AND sessions.updated_utc < ?
		  AND locks.session_id IS NULL
		ORDER BY sessions.updated_utc, sessions.id
		LIMIT ?
	`, formatTimestamp(before), limit)
	if err != nil {
		return nil, fmt.Errorf("select expired sessions: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan expired session: %w", err)
		}
		ids = append(ids, sessionID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close expired session rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired sessions: %w", err)
	}
	for _, sessionID := range ids {
		for _, statement := range []string{
			"DELETE FROM session_asset_baseline_captures WHERE session_id = ?",
			"DELETE FROM audit_events WHERE session_id = ?",
			"DELETE FROM event_chain_checkpoints WHERE session_id = ?",
			"DELETE FROM session_retention_locks WHERE session_id = ?",
			"DELETE FROM sessions WHERE id = ?",
		} {
			if _, err := tx.ExecContext(ctx, statement, sessionID); err != nil {
				return nil, fmt.Errorf("delete retained session %s: %w", sessionID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session retention transaction: %w", err)
	}
	return ids, nil
}
