package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"desktopguardpro/internal/domain"
)

var ErrSessionHistoryCursor = errors.New("invalid session history cursor")
var ErrSessionHistoryEntryTooLarge = errors.New("session history entry exceeds response budget")

type SessionListItem struct {
	Session         domain.Session `json:"session"`
	CreatedUTC      time.Time      `json:"createdUtc"`
	UpdatedUTC      time.Time      `json:"updatedUtc"`
	RetentionLocked bool           `json:"retentionLocked"`
}

type SessionListPage struct {
	Items      []SessionListItem `json:"items"`
	HasMore    bool              `json:"hasMore"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

func (repository *Repository) ListSessions(ctx context.Context, cursor string) (SessionListPage, error) {
	const pageSize = 50
	const pageByteBudget = 512 * 1024
	query := `
		SELECT sessions.rowid, sessions.id, substr(sessions.name,1,200), sessions.state,
			sessions.revision, sessions.monitoring_level, sessions.monitoring_policy, sessions.created_utc, sessions.updated_utc,
			CASE WHEN locks.session_id IS NULL THEN 0 ELSE 1 END
		FROM sessions
		LEFT JOIN session_retention_locks locks ON locks.session_id = sessions.id
	`
	var args []any
	if cursor != "" {
		before, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || before < 1 {
			return SessionListPage{}, ErrSessionHistoryCursor
		}
		query += " WHERE sessions.rowid < ?"
		args = append(args, before)
	}
	query += " ORDER BY sessions.rowid DESC LIMIT 51"
	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return SessionListPage{}, err
	}
	defer rows.Close()
	page := SessionListPage{Items: []SessionListItem{}}
	// Reserve space for the page fields, cursor, and IPC response envelope.
	encodedBytes := 1024
	for rows.Next() {
		if len(page.Items) == pageSize {
			page.HasMore = true
			break
		}
		var item SessionListItem
		var rowID int64
		var policyJSON, created, updated string
		if err := rows.Scan(&rowID, &item.Session.ID, &item.Session.Name, &item.Session.State, &item.Session.Revision, &item.Session.MonitoringLevel, &policyJSON, &created, &updated, &item.RetentionLocked); err != nil {
			return SessionListPage{}, err
		}
		var err error
		if item.Session.MonitoringPolicy, err = decodeSessionMonitoringPolicy(policyJSON); err != nil {
			return SessionListPage{}, err
		}
		if err := validateSession(item.Session); err != nil {
			return SessionListPage{}, err
		}
		item.CreatedUTC, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return SessionListPage{}, err
		}
		item.UpdatedUTC, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return SessionListPage{}, err
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return SessionListPage{}, err
		}
		if encodedBytes+len(encoded)+1 > pageByteBudget {
			if len(page.Items) == 0 {
				return SessionListPage{}, ErrSessionHistoryEntryTooLarge
			}
			page.HasMore = true
			break
		}
		encodedBytes += len(encoded) + 1
		page.Items = append(page.Items, item)
		page.NextCursor = strconv.FormatInt(rowID, 10)
	}
	if !page.HasMore {
		page.NextCursor = ""
	}
	return page, rows.Err()
}
