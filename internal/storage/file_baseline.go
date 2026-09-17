package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrFileBaselineNotFound = errors.New("file baseline is not found")
	ErrFileBaselineStage    = errors.New("file baseline stage is invalid")
	ErrFileBaselineEntry    = errors.New("file baseline entry is invalid")
)

type FileBaselineStage string

const (
	FileBaselineStageStart    FileBaselineStage = "start"
	FileBaselineStageEnd      FileBaselineStage = "end"
	FileBaselineStageRecovery FileBaselineStage = "recovery"
)

type FileBaselineEntry struct {
	Path          string
	Size          int64
	Mode          uint32
	CreatedUTC    time.Time
	AccessedUTC   time.Time
	ModifiedUTC   time.Time
	ContentSHA256 string
	HashStatus    string
}

func (repository *Repository) StoreFileBaseline(ctx context.Context, sessionID string, stage FileBaselineStage, entries []FileBaselineEntry) error {
	if !stage.valid() {
		return ErrFileBaselineStage
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}
	for _, entry := range entries {
		if err := entry.validate(); err != nil {
			return err
		}
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin file baseline transaction: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", sessionID).Scan(&exists); err != nil {
		return fmt.Errorf("check file baseline session: %w", err)
	}
	if exists == 0 {
		return ErrSessionNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_file_baseline_captures (session_id, capture_stage)
		VALUES (?, ?) ON CONFLICT(session_id, capture_stage) DO NOTHING
	`, sessionID, stage); err != nil {
		return fmt.Errorf("record file baseline capture: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM session_file_baselines WHERE session_id = ? AND capture_stage = ?", sessionID, stage); err != nil {
		return fmt.Errorf("replace file baseline: %w", err)
	}
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_file_baselines (session_id, capture_stage, path, size, mode, created_utc, accessed_utc, modified_utc, content_sha256, hash_status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, sessionID, stage, entry.Path, entry.Size, entry.Mode, formatTimestamp(entry.CreatedUTC), formatTimestamp(entry.AccessedUTC),
			formatTimestamp(entry.ModifiedUTC), entry.ContentSHA256, entry.HashStatus); err != nil {
			return fmt.Errorf("store file baseline entry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit file baseline transaction: %w", err)
	}
	return nil
}

func (repository *Repository) LoadFileBaseline(ctx context.Context, sessionID string, stage FileBaselineStage) ([]FileBaselineEntry, error) {
	if !stage.valid() {
		return nil, ErrFileBaselineStage
	}
	if _, err := repository.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	var captured int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM session_file_baseline_captures WHERE session_id = ? AND capture_stage = ?)
	`, sessionID, stage).Scan(&captured); err != nil {
		return nil, fmt.Errorf("query file baseline capture: %w", err)
	}
	if captured == 0 {
		return nil, ErrFileBaselineNotFound
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT path, size, mode, created_utc, accessed_utc, modified_utc, content_sha256, hash_status
		FROM session_file_baselines WHERE session_id = ? AND capture_stage = ? ORDER BY path
	`, sessionID, stage)
	if err != nil {
		return nil, fmt.Errorf("query file baseline: %w", err)
	}
	defer rows.Close()
	entries := []FileBaselineEntry{}
	for rows.Next() {
		var entry FileBaselineEntry
		var created, accessed, modified string
		if err := rows.Scan(&entry.Path, &entry.Size, &entry.Mode, &created, &accessed, &modified, &entry.ContentSHA256, &entry.HashStatus); err != nil {
			return nil, fmt.Errorf("scan file baseline entry: %w", err)
		}
		if entry.CreatedUTC, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("parse file baseline creation timestamp: %w", err)
		}
		if entry.AccessedUTC, err = time.Parse(time.RFC3339Nano, accessed); err != nil {
			return nil, fmt.Errorf("parse file baseline access timestamp: %w", err)
		}
		if entry.ModifiedUTC, err = time.Parse(time.RFC3339Nano, modified); err != nil {
			return nil, fmt.Errorf("parse file baseline timestamp: %w", err)
		}
		if err := entry.validate(); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file baseline: %w", err)
	}
	return entries, nil
}

func (stage FileBaselineStage) valid() bool {
	return stage == FileBaselineStageStart || stage == FileBaselineStageEnd || stage == FileBaselineStageRecovery
}

func (entry FileBaselineEntry) validate() error {
	if strings.TrimSpace(entry.Path) == "" || entry.Size < 0 || entry.CreatedUTC.IsZero() || entry.AccessedUTC.IsZero() || entry.ModifiedUTC.IsZero() || strings.TrimSpace(entry.HashStatus) == "" {
		return ErrFileBaselineEntry
	}
	_, createdOffset := entry.CreatedUTC.Zone()
	_, accessedOffset := entry.AccessedUTC.Zone()
	_, modifiedOffset := entry.ModifiedUTC.Zone()
	if createdOffset != 0 || accessedOffset != 0 || modifiedOffset != 0 {
		return ErrFileBaselineEntry
	}
	return nil
}
