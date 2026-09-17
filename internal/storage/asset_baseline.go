package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"desktopguardpro/internal/domain"
)

var (
	ErrAssetBaselineNotFound = errors.New("asset baseline not found")
	ErrAssetBaselineStage    = errors.New("asset baseline stage is invalid")
)

type AssetBaselineStage string

const (
	AssetBaselineStageStart AssetBaselineStage = "start"
	AssetBaselineStageEnd   AssetBaselineStage = "end"
)

func (repository *Repository) StoreAssetBaseline(
	ctx context.Context,
	sessionID string,
	stage AssetBaselineStage,
	baseline domain.AssetBaseline,
) error {
	if !stage.valid() {
		return ErrAssetBaselineStage
	}
	if err := baseline.Validate(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin asset baseline transaction: %w", err)
	}
	defer tx.Rollback()

	var sessionExists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", sessionID).Scan(&sessionExists); err != nil {
		return fmt.Errorf("check asset baseline session: %w", err)
	}
	if sessionExists != 1 {
		return ErrSessionNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_asset_baseline_captures (session_id, capture_stage)
		VALUES (?, ?)
		ON CONFLICT (session_id, capture_stage) DO NOTHING
	`, sessionID, stage); err != nil {
		return fmt.Errorf("record asset baseline capture: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM session_asset_baselines
		WHERE session_id = ? AND capture_stage = ?
	`, sessionID, stage); err != nil {
		return fmt.Errorf("replace asset baseline: %w", err)
	}
	for _, asset := range baseline.Assets {
		attributes, err := marshalAssetAttributes(asset.Attributes)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_asset_baselines (
				session_id, capture_stage, category, identifier, display_name, attributes_json
			) VALUES (?, ?, ?, ?, ?, ?)
		`, sessionID, stage, asset.Category, asset.Identifier, asset.DisplayName, attributes); err != nil {
			return fmt.Errorf("insert asset baseline item: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit asset baseline transaction: %w", err)
	}
	return nil
}

func (repository *Repository) LoadAssetBaseline(
	ctx context.Context,
	sessionID string,
	stage AssetBaselineStage,
) (domain.AssetBaseline, error) {
	if !stage.valid() {
		return domain.AssetBaseline{}, ErrAssetBaselineStage
	}
	if _, err := repository.GetSession(ctx, sessionID); err != nil {
		return domain.AssetBaseline{}, err
	}

	var captured int
	err := repository.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM session_asset_baseline_captures
			WHERE session_id = ? AND capture_stage = ?
		)
	`, sessionID, stage).Scan(&captured)
	if err != nil {
		return domain.AssetBaseline{}, fmt.Errorf("query asset baseline capture: %w", err)
	}
	if captured != 1 {
		return domain.AssetBaseline{}, ErrAssetBaselineNotFound
	}

	rows, err := repository.db.QueryContext(ctx, `
		SELECT category, identifier, display_name, attributes_json
		FROM session_asset_baselines
		WHERE session_id = ? AND capture_stage = ?
		ORDER BY category, identifier
	`, sessionID, stage)
	if err != nil {
		return domain.AssetBaseline{}, fmt.Errorf("query asset baseline: %w", err)
	}
	defer rows.Close()

	baseline := domain.AssetBaseline{}
	for rows.Next() {
		var asset domain.Asset
		var encodedAttributes string
		if err := rows.Scan(&asset.Category, &asset.Identifier, &asset.DisplayName, &encodedAttributes); err != nil {
			return domain.AssetBaseline{}, fmt.Errorf("scan asset baseline item: %w", err)
		}
		if err := json.Unmarshal([]byte(encodedAttributes), &asset.Attributes); err != nil {
			return domain.AssetBaseline{}, fmt.Errorf("decode asset baseline attributes: %w", err)
		}
		baseline.Assets = append(baseline.Assets, asset)
	}
	if err := rows.Err(); err != nil {
		return domain.AssetBaseline{}, fmt.Errorf("iterate asset baseline: %w", err)
	}
	if err := baseline.Validate(); err != nil {
		return domain.AssetBaseline{}, fmt.Errorf("stored asset baseline is invalid: %w", err)
	}
	return baseline, nil
}

func (stage AssetBaselineStage) valid() bool {
	return stage == AssetBaselineStageStart || stage == AssetBaselineStageEnd
}

func marshalAssetAttributes(attributes map[string]string) (string, error) {
	if len(attributes) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(attributes)
	if err != nil {
		return "", fmt.Errorf("encode asset baseline attributes: %w", err)
	}
	return string(encoded), nil
}
