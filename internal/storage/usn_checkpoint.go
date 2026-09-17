package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrUSNJournalCheckpointInvalid = errors.New("USN journal checkpoint is invalid")

type USNJournalCheckpoint struct {
	SessionID   string
	Stage       USNCheckpointStage
	Volume      string
	JournalID   uint64
	NextUSN     uint64
	CapturedUTC time.Time
}

type USNCheckpointStage string

const (
	USNCheckpointStageStart    USNCheckpointStage = "start"
	USNCheckpointStageEnd      USNCheckpointStage = "end"
	USNCheckpointStageRecovery USNCheckpointStage = "recovery"
)

func (repository *Repository) StoreUSNJournalCheckpoints(ctx context.Context, checkpoints []USNJournalCheckpoint) error {
	if len(checkpoints) == 0 {
		return nil
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin USN checkpoint transaction: %w", err)
	}
	defer tx.Rollback()
	for _, checkpoint := range checkpoints {
		if err := checkpoint.validate(); err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", checkpoint.SessionID).Scan(&exists); err != nil {
			return fmt.Errorf("check USN checkpoint session: %w", err)
		}
		if exists == 0 {
			return ErrSessionNotFound
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_usn_checkpoints (session_id, capture_stage, volume, journal_id, next_usn, captured_utc)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, capture_stage, volume) DO UPDATE SET
				journal_id = excluded.journal_id,
				next_usn = excluded.next_usn,
				captured_utc = excluded.captured_utc
		`, checkpoint.SessionID, checkpoint.Stage, checkpoint.Volume, int64(checkpoint.JournalID), int64(checkpoint.NextUSN), formatTimestamp(checkpoint.CapturedUTC)); err != nil {
			return fmt.Errorf("store USN checkpoint: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit USN checkpoint transaction: %w", err)
	}
	return nil
}

func (repository *Repository) LoadUSNJournalCheckpoints(ctx context.Context, sessionID string, stage USNCheckpointStage) ([]USNJournalCheckpoint, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !stage.valid() {
		return nil, ErrSessionNotFound
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT volume, journal_id, next_usn, captured_utc
		FROM session_usn_checkpoints WHERE session_id = ? AND capture_stage = ? ORDER BY volume
	`, sessionID, stage)
	if err != nil {
		return nil, fmt.Errorf("query USN checkpoints: %w", err)
	}
	defer rows.Close()
	result := []USNJournalCheckpoint{}
	for rows.Next() {
		var checkpoint USNJournalCheckpoint
		var journalID, nextUSN int64
		var captured string
		checkpoint.SessionID, checkpoint.Stage = sessionID, stage
		if err := rows.Scan(&checkpoint.Volume, &journalID, &nextUSN, &captured); err != nil {
			return nil, fmt.Errorf("scan USN checkpoint: %w", err)
		}
		checkpoint.JournalID, checkpoint.NextUSN = uint64(journalID), uint64(nextUSN)
		if checkpoint.CapturedUTC, err = time.Parse(time.RFC3339Nano, captured); err != nil {
			return nil, fmt.Errorf("parse USN checkpoint timestamp: %w", err)
		}
		result = append(result, checkpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate USN checkpoints: %w", err)
	}
	return result, nil
}

func (checkpoint USNJournalCheckpoint) validate() error {
	if strings.TrimSpace(checkpoint.SessionID) == "" || !checkpoint.Stage.valid() || strings.TrimSpace(checkpoint.Volume) == "" ||
		checkpoint.JournalID > uint64(^uint64(0)>>1) || checkpoint.NextUSN > uint64(^uint64(0)>>1) || checkpoint.CapturedUTC.IsZero() {
		return ErrUSNJournalCheckpointInvalid
	}
	_, offset := checkpoint.CapturedUTC.Zone()
	if offset != 0 {
		return ErrUSNJournalCheckpointInvalid
	}
	return nil
}

func (stage USNCheckpointStage) valid() bool {
	return stage == USNCheckpointStageStart || stage == USNCheckpointStageEnd || stage == USNCheckpointStageRecovery
}
