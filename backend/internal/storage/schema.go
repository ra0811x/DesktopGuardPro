package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	busyTimeoutMilliseconds = 5_000
	currentSchemaVersion    = 13
)

var (
	ErrDatabasePathRequired = errors.New("database path is required")
	ErrSchemaTooNew         = errors.New("database schema is newer than this application")
)

var migrations = []string{
	`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN (
			'draft', 'preparing', 'baseline_review', 'active', 'degraded',
			'finalizing', 'completed', 'failed'
		)),
		revision INTEGER NOT NULL CHECK (revision >= 0),
		created_utc TEXT NOT NULL,
		updated_utc TEXT NOT NULL
	) STRICT;

	CREATE TABLE audit_events (
		event_id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		sequence INTEGER NOT NULL CHECK (sequence > 0),
		category TEXT NOT NULL CHECK (category IN (
			'file', 'process', 'software', 'system', 'device', 'health'
		)),
		action TEXT NOT NULL,
		severity TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high')),
		observed_utc TEXT NOT NULL,
		monotonic_ticks INTEGER NOT NULL,
		windows_session_id INTEGER,
		user_sid_hash BLOB,
		process_key TEXT,
		object_key TEXT,
		source TEXT NOT NULL,
		confidence TEXT NOT NULL CHECK (confidence IN (
			'direct', 'correlated', 'snapshot_diff'
		)),
		payload_nonce BLOB NOT NULL,
		payload_ciphertext BLOB NOT NULL,
		previous_hash BLOB NOT NULL,
		event_hash BLOB NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		UNIQUE (session_id, sequence)
	) STRICT;

	CREATE INDEX audit_events_session_observed
	ON audit_events(session_id, observed_utc);`,
	`CREATE TABLE event_chain_checkpoints (
		session_id TEXT PRIMARY KEY,
		event_count INTEGER NOT NULL CHECK (event_count >= 0),
		tail_hash BLOB NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE RESTRICT
	) STRICT;

	INSERT INTO event_chain_checkpoints (session_id, event_count, tail_hash)
	SELECT s.id,
		COALESCE((SELECT MAX(e.sequence) FROM audit_events e WHERE e.session_id = s.id), 0),
		COALESCE((
			SELECT e.event_hash FROM audit_events e
			WHERE e.session_id = s.id
			ORDER BY e.sequence DESC
			LIMIT 1
		), zeroblob(32))
	FROM sessions s;`,
	`CREATE TABLE directory_monitoring (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		nonce BLOB NOT NULL,
		ciphertext BLOB NOT NULL
	) STRICT;`,
	`ALTER TABLE audit_events ADD COLUMN keys_encrypted INTEGER NOT NULL DEFAULT 0 CHECK (keys_encrypted IN (0, 1));
	CREATE TABLE sensitive_key_cleanup (id INTEGER PRIMARY KEY CHECK (id = 1)) STRICT;
	INSERT INTO sensitive_key_cleanup VALUES (1);`,
	`CREATE TABLE IF NOT EXISTS session_asset_baseline_captures (
		session_id TEXT NOT NULL,
		capture_stage TEXT NOT NULL CHECK (capture_stage IN ('start', 'end')),
		PRIMARY KEY (session_id, capture_stage),
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE RESTRICT
	) STRICT;

	CREATE TABLE IF NOT EXISTS session_asset_baselines (
		session_id TEXT NOT NULL,
		capture_stage TEXT NOT NULL CHECK (capture_stage IN ('start', 'end')),
		category TEXT NOT NULL CHECK (category IN (
			'software', 'device', 'network', 'account', 'system'
		)),
		identifier TEXT NOT NULL,
		display_name TEXT NOT NULL,
		attributes_json TEXT NOT NULL CHECK (json_valid(attributes_json)),
		PRIMARY KEY (session_id, capture_stage, category, identifier),
		FOREIGN KEY (session_id, capture_stage)
			REFERENCES session_asset_baseline_captures(session_id, capture_stage) ON DELETE CASCADE
	) STRICT;

	CREATE INDEX IF NOT EXISTS session_asset_baselines_session_stage
	ON session_asset_baselines(session_id, capture_stage);`,
	`CREATE TABLE IF NOT EXISTS session_retention_locks (
		session_id TEXT PRIMARY KEY,
		locked_utc TEXT NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	) STRICT;

	CREATE INDEX IF NOT EXISTS session_retention_locks_session
	ON session_retention_locks(session_id);`,
	`CREATE TABLE IF NOT EXISTS session_baseline_reviews (
		session_id TEXT PRIMARY KEY,
		status TEXT NOT NULL CHECK (status IN ('partial_failure', 'failed')),
		attempted_item_count INTEGER NOT NULL CHECK (attempted_item_count >= 0),
		succeeded_item_count INTEGER NOT NULL CHECK (succeeded_item_count >= 0),
		failures_json TEXT NOT NULL CHECK (json_valid(failures_json)),
		resolution TEXT CHECK (resolution IN ('continue', 'cancel')),
		created_utc TEXT NOT NULL,
		resolved_utc TEXT,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	) STRICT;

	CREATE INDEX IF NOT EXISTS session_baseline_reviews_resolution
	ON session_baseline_reviews(resolution);`,
	`ALTER TABLE sessions ADD COLUMN monitoring_level TEXT NOT NULL DEFAULT 'standard'
		CHECK (monitoring_level IN ('standard', 'strict'));`,
	`CREATE TABLE IF NOT EXISTS session_usn_checkpoints (
		session_id TEXT NOT NULL,
		capture_stage TEXT NOT NULL CHECK (capture_stage IN ('start', 'end', 'recovery')),
		volume TEXT NOT NULL,
		journal_id INTEGER NOT NULL CHECK (journal_id >= 0),
		next_usn INTEGER NOT NULL CHECK (next_usn >= 0),
		captured_utc TEXT NOT NULL,
		PRIMARY KEY (session_id, capture_stage, volume),
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	) STRICT;`,
	`CREATE TABLE IF NOT EXISTS session_file_baseline_captures (
		session_id TEXT NOT NULL,
		capture_stage TEXT NOT NULL CHECK (capture_stage IN ('start', 'end', 'recovery')),
		PRIMARY KEY (session_id, capture_stage),
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	) STRICT;
	CREATE TABLE IF NOT EXISTS session_file_baselines (
		session_id TEXT NOT NULL,
		capture_stage TEXT NOT NULL CHECK (capture_stage IN ('start', 'end', 'recovery')),
		path TEXT NOT NULL,
		size INTEGER NOT NULL CHECK (size >= 0),
		mode INTEGER NOT NULL CHECK (mode >= 0),
		modified_utc TEXT NOT NULL,
		content_sha256 TEXT NOT NULL,
		hash_status TEXT NOT NULL,
		PRIMARY KEY (session_id, capture_stage, path),
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	) STRICT;
	CREATE INDEX IF NOT EXISTS session_file_baselines_session_stage
		ON session_file_baselines(session_id, capture_stage);`,
	``,
	`ALTER TABLE sessions ADD COLUMN monitoring_policy TEXT NOT NULL DEFAULT '{"fileActivityEnabled":true,"processAndSoftwareEnabled":true,"systemAndNetworkEnabled":true,"externalDevicesEnabled":true,"userSessionActivityEnabled":true,"strictReadAuditEnabled":false}'
		CHECK (json_valid(monitoring_policy));`,
	`CREATE TABLE IF NOT EXISTS input_shield_credentials (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		nonce BLOB NOT NULL,
		ciphertext BLOB NOT NULL
	) STRICT;`,
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrDatabasePathRequired
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", databaseSourceName(absPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to sqlite database: %w", err)
	}
	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func databaseSourceName(path string) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout("+strconv.Itoa(busyTimeoutMilliseconds)+")")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "secure_delete(ON)")

	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}

	return (&url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: query.Encode(),
	}).String()
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("%w: database=%d application=%d", ErrSchemaTooNew, version, currentSchemaVersion)
	}

	for version < currentSchemaVersion {
		nextVersion := version + 1
		if nextVersion == 7 {
			if err := migrateBaselineReviewSchema(ctx, conn); err != nil {
				return err
			}
			version = nextVersion
			continue
		}
		if nextVersion == 11 {
			if err := migratePausedSessionsAndFileTimestamps(ctx, conn); err != nil {
				return err
			}
			version = nextVersion
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", nextVersion, err)
		}

		if _, err := tx.ExecContext(ctx, migrations[version]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", nextVersion, err)
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(nextVersion)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", nextVersion, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", nextVersion, err)
		}
		version = nextVersion
	}

	return nil
}

func migratePausedSessionsAndFileTimestamps(ctx context.Context, conn *sql.Conn) error {
	createdExists, err := tableColumnExists(ctx, conn, "session_file_baselines", "created_utc")
	if err != nil {
		return err
	}
	accessedExists, err := tableColumnExists(ctx, conn, "session_file_baselines", "accessed_utc")
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys for migration 11: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON") }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 11: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE sessions_replacement (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN (
				'draft', 'preparing', 'baseline_review', 'active', 'paused', 'degraded',
				'finalizing', 'completed', 'failed'
			)),
			revision INTEGER NOT NULL CHECK (revision >= 0),
			created_utc TEXT NOT NULL,
			updated_utc TEXT NOT NULL,
			monitoring_level TEXT NOT NULL DEFAULT 'standard' CHECK (monitoring_level IN ('standard', 'strict'))
		) STRICT;
		INSERT INTO sessions_replacement (id, name, state, revision, created_utc, updated_utc, monitoring_level)
		SELECT id, name, state, revision, created_utc, updated_utc, monitoring_level FROM sessions;
		DROP TABLE sessions;
		ALTER TABLE sessions_replacement RENAME TO sessions;
	`); err != nil {
		return fmt.Errorf("apply migration 11: %w", err)
	}
	if !createdExists {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE session_file_baselines ADD COLUMN created_utc TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z'"); err != nil {
			return fmt.Errorf("add file creation timestamp: %w", err)
		}
	}
	if !accessedExists {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE session_file_baselines ADD COLUMN accessed_utc TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z'"); err != nil {
			return fmt.Errorf("add file access timestamp: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE session_file_baselines SET created_utc = modified_utc, accessed_utc = modified_utc WHERE created_utc = '1970-01-01T00:00:00Z' OR accessed_utc = '1970-01-01T00:00:00Z'"); err != nil {
		return fmt.Errorf("backfill file timestamps: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 11"); err != nil {
		return fmt.Errorf("record migration 11: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 11: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys after migration 11: %w", err)
	}
	var table string
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_key_check").Scan(&table); err != sql.ErrNoRows {
		if err != nil {
			return fmt.Errorf("verify migration 11 foreign keys: %w", err)
		}
		return errors.New("foreign key check failed after migration 11")
	}
	return nil
}

func tableColumnExists(ctx context.Context, conn *sql.Conn, table, column string) (bool, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, fmt.Errorf("inspect table %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func migrateBaselineReviewSchema(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys for baseline review migration: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON") }()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 7: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE sessions_replacement (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN (
				'draft', 'preparing', 'baseline_review', 'active', 'degraded',
				'finalizing', 'completed', 'failed'
			)),
			revision INTEGER NOT NULL CHECK (revision >= 0),
			created_utc TEXT NOT NULL,
			updated_utc TEXT NOT NULL
		) STRICT;
		INSERT INTO sessions_replacement (id, name, state, revision, created_utc, updated_utc)
		SELECT id, name, state, revision, created_utc, updated_utc FROM sessions;
		DROP TABLE sessions;
		ALTER TABLE sessions_replacement RENAME TO sessions;
	`); err != nil {
		return fmt.Errorf("rebuild sessions for baseline review migration: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migrations[6]); err != nil {
		return fmt.Errorf("apply migration 7: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		return fmt.Errorf("record migration 7: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 7: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys after baseline review migration: %w", err)
	}
	var table string
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_key_check").Scan(&table); err != sql.ErrNoRows {
		if err != nil {
			return fmt.Errorf("verify foreign keys after baseline review migration: %w", err)
		}
		return errors.New("foreign key check failed after baseline review migration")
	}
	return nil
}
