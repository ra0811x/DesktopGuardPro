package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"desktopguardpro/internal/domain"
)

// This diagnostic read never creates a database, migrates a schema or generates
// keys. Callers must first validate the installation owner and directory ACL.
func ReadPersistedProtectionSession(ctx context.Context, path string) (domain.Session, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return domain.Session{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return domain.Session{}, false, errors.New("invalid persisted protection database")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return domain.Session{}, false, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !strings.EqualFold(resolved, absolute) {
		return domain.Session{}, false, errors.New("persisted protection database resolves outside its path")
	}
	uri, err := url.Parse(databaseSourceName(absolute))
	if err != nil {
		return domain.Session{}, false, err
	}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "query_only(ON)")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return domain.Session{}, false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var hasMonitoringLevel bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'monitoring_level'
		)
	`).Scan(&hasMonitoringLevel); err != nil {
		return domain.Session{}, false, err
	}
	columns := "id, name, state, revision"
	if hasMonitoringLevel {
		columns += ", monitoring_level"
	}
	rows, err := db.QueryContext(ctx, "SELECT "+columns+" FROM sessions WHERE state NOT IN ('completed', 'failed') LIMIT 2")
	if err != nil {
		return domain.Session{}, false, err
	}
	defer rows.Close()
	var session domain.Session
	found := false
	for rows.Next() {
		if found {
			return domain.Session{}, false, ErrMultipleRecoverableSessions
		}
		session.MonitoringLevel = domain.MonitoringLevelStandard
		destinations := []any{&session.ID, &session.Name, &session.State, &session.Revision}
		if hasMonitoringLevel {
			destinations = append(destinations, &session.MonitoringLevel)
		}
		if err := rows.Scan(destinations...); err != nil {
			return domain.Session{}, false, err
		}
		if err := validateSession(session); err != nil {
			return domain.Session{}, false, err
		}
		found = true
	}
	return session, found, rows.Err()
}
