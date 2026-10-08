package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/maintenancegate"
)

var (
	ErrRepositoryDependency        = errors.New("repository dependency is required")
	ErrSessionNotFound             = errors.New("session not found")
	ErrSessionRevisionConflict     = errors.New("session revision conflict")
	ErrEventSequenceConflict       = errors.New("event sequence conflict")
	ErrEventChain                  = errors.New("event hash chain is invalid")
	ErrMultipleRecoverableSessions = errors.New("multiple recoverable sessions found")
	ErrBaselineReviewNotFound      = errors.New("baseline review not found")
	ErrBaselineReviewNotRequired   = errors.New("baseline review is not required")
	ErrBaselineReviewResolved      = errors.New("baseline review is already resolved")
	ErrBaselineReviewResolution    = errors.New("baseline review resolution is invalid")
)

type BaselineReviewResolution string

const (
	BaselineReviewResolutionContinue BaselineReviewResolution = "continue"
	BaselineReviewResolutionCancel   BaselineReviewResolution = "cancel"
)

type BaselineReview struct {
	SessionID  string                       `json:"sessionId"`
	Decision   domain.BaselineStartDecision `json:"decision"`
	Resolution BaselineReviewResolution     `json:"resolution,omitempty"`
}

type EventRecord struct {
	Event            domain.AuditEvent
	Payload          []byte
	PreviewTruncated bool `json:"previewTruncated,omitempty"`
}

type eventChainCheckpoint struct {
	eventCount uint64
	tailHash   []byte
	commitment []byte
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	dataDirectory string
	db            *sql.DB
	cipher        *PayloadCipher
	integrity     *EventIntegrity
	eventAppendMu sync.Mutex
}

func NewRepository(db *sql.DB, cipher *PayloadCipher, integrity *EventIntegrity) (*Repository, error) {
	if db == nil || cipher == nil || integrity == nil {
		return nil, ErrRepositoryDependency
	}
	repository := &Repository{db: db, cipher: cipher, integrity: integrity}
	var databasePath string
	if err := db.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&databasePath); err != nil || databasePath == "" {
		return nil, fmt.Errorf("resolve persistent session maintenance gate: %v", err)
	}
	repository.dataDirectory = filepath.Dir(databasePath)
	if err := repository.initializeEventChainCheckpoints(context.Background()); err != nil {
		return nil, fmt.Errorf("initialize event chain checkpoints: %w", err)
	}
	if err := repository.migrateSensitiveEventKeys(context.Background()); err != nil {
		return nil, fmt.Errorf("encrypt sensitive event keys: %w", err)
	}
	return repository, nil
}

func (repository *Repository) CreateSession(ctx context.Context, session domain.Session, at time.Time) error {
	if err := validateSession(session); err != nil {
		return err
	}
	if session.Revision != 0 || session.State != domain.SessionStateDraft {
		return ErrSessionRevisionConflict
	}
	gate, err := maintenancegate.Acquire(ctx, repository.dataDirectory)
	if err != nil {
		return err
	}
	defer gate.Close()
	if err := gate.CheckReady(); err != nil {
		return err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session transaction: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatTimestamp(at)
	policyJSON, err := encodeSessionMonitoringPolicy(session)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, name, state, revision, monitoring_level, monitoring_policy, created_utc, updated_utc)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, session.ID, session.Name, session.State, session.Revision, session.MonitoringLevel, policyJSON, timestamp, timestamp)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	checkpoint, err := repository.newEventChainCheckpoint(session.ID, 0, make([]byte, eventHashSize))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO event_chain_checkpoints (session_id, event_count, tail_hash)
		VALUES (?, 0, ?)
	`, session.ID, checkpoint.databaseValue())
	if err != nil {
		return fmt.Errorf("insert event chain checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session transaction: %w", err)
	}
	return nil
}

func (repository *Repository) UpdateSession(ctx context.Context, session domain.Session, at time.Time) error {
	if err := validateSession(session); err != nil {
		return err
	}
	if session.Revision == 0 || session.Revision > math.MaxInt64 {
		return ErrSessionRevisionConflict
	}

	policyJSON, err := encodeSessionMonitoringPolicy(session)
	if err != nil {
		return err
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE sessions
		SET name = ?, state = ?, revision = ?, monitoring_level = ?, monitoring_policy = ?, updated_utc = ?
		WHERE id = ? AND revision = ?
	`, session.Name, session.State, session.Revision, session.MonitoringLevel, policyJSON, formatTimestamp(at), session.ID, session.Revision-1)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated session count: %w", err)
	}
	if rows != 1 {
		return ErrSessionRevisionConflict
	}
	return nil
}

func (repository *Repository) StoreBaselineReview(
	ctx context.Context,
	sessionID string,
	result domain.BaselineCaptureResult,
) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}
	decision, err := result.StartDecision()
	if err != nil {
		return err
	}
	if !decision.RequiresUserChoice {
		return ErrBaselineReviewNotRequired
	}
	failures, err := json.Marshal(decision.Failures)
	if err != nil {
		return fmt.Errorf("encode baseline review failures: %w", err)
	}

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin baseline review transaction: %w", err)
	}
	defer tx.Rollback()
	var sessionExists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", sessionID).Scan(&sessionExists); err != nil {
		return fmt.Errorf("check baseline review session: %w", err)
	}
	if sessionExists != 1 {
		return ErrSessionNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_baseline_reviews (
			session_id, status, attempted_item_count, succeeded_item_count,
			failures_json, resolution, created_utc, resolved_utc
		) VALUES (?, ?, ?, ?, ?, NULL, ?, NULL)
		ON CONFLICT (session_id) DO UPDATE SET
			status = excluded.status,
			attempted_item_count = excluded.attempted_item_count,
			succeeded_item_count = excluded.succeeded_item_count,
			failures_json = excluded.failures_json,
			resolution = NULL,
			created_utc = excluded.created_utc,
			resolved_utc = NULL
	`, sessionID, decision.Status, result.AttemptedItemCount, result.SucceededItemCount, string(failures), formatTimestamp(time.Now())); err != nil {
		return fmt.Errorf("store baseline review: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit baseline review transaction: %w", err)
	}
	return nil
}

func (repository *Repository) LoadBaselineReview(ctx context.Context, sessionID string) (BaselineReview, error) {
	sessionID = strings.TrimSpace(sessionID)
	if _, err := repository.GetSession(ctx, sessionID); err != nil {
		return BaselineReview{}, err
	}

	var status domain.BaselineCaptureStatus
	var attempted, succeeded int
	var failuresJSON string
	var resolution sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT status, attempted_item_count, succeeded_item_count, failures_json, resolution
		FROM session_baseline_reviews WHERE session_id = ?
	`, sessionID).Scan(&status, &attempted, &succeeded, &failuresJSON, &resolution)
	if errors.Is(err, sql.ErrNoRows) {
		return BaselineReview{}, ErrBaselineReviewNotFound
	}
	if err != nil {
		return BaselineReview{}, fmt.Errorf("load baseline review: %w", err)
	}

	var failures []domain.BaselineCaptureFailure
	if err := json.Unmarshal([]byte(failuresJSON), &failures); err != nil {
		return BaselineReview{}, fmt.Errorf("decode baseline review failures: %w", err)
	}
	decision, err := (domain.BaselineCaptureResult{
		AttemptedItemCount: attempted,
		SucceededItemCount: succeeded,
		Failures:           failures,
	}).StartDecision()
	if err != nil || decision.Status != status || !decision.RequiresUserChoice {
		return BaselineReview{}, errors.New("stored baseline review is invalid")
	}

	review := BaselineReview{SessionID: sessionID, Decision: decision}
	if resolution.Valid {
		review.Resolution = BaselineReviewResolution(resolution.String)
		if !review.Resolution.valid() {
			return BaselineReview{}, errors.New("stored baseline review resolution is invalid")
		}
	}
	return review, nil
}

func (repository *Repository) ResolveBaselineReview(
	ctx context.Context,
	sessionID string,
	resolution BaselineReviewResolution,
) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}
	if !resolution.valid() {
		return ErrBaselineReviewResolution
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE session_baseline_reviews
		SET resolution = ?, resolved_utc = ?
		WHERE session_id = ? AND resolution IS NULL
	`, resolution, formatTimestamp(time.Now()), sessionID)
	if err != nil {
		return fmt.Errorf("resolve baseline review: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read baseline review resolution count: %w", err)
	}
	if rows == 1 {
		return nil
	}
	if _, err := repository.LoadBaselineReview(ctx, sessionID); errors.Is(err, ErrBaselineReviewNotFound) {
		return ErrBaselineReviewNotFound
	} else if err != nil {
		return err
	}
	return ErrBaselineReviewResolved
}

func (repository *Repository) ResolveBaselineReviewAndUpdateSessionAndAppendEvent(
	ctx context.Context,
	session domain.Session,
	at time.Time,
	resolution BaselineReviewResolution,
	event domain.AuditEvent,
	payload []byte,
) (domain.AuditEvent, error) {
	if err := validateSession(session); err != nil {
		return domain.AuditEvent{}, err
	}
	if session.Revision == 0 || session.Revision > math.MaxInt64 {
		return domain.AuditEvent{}, ErrSessionRevisionConflict
	}
	if !resolution.valid() {
		return domain.AuditEvent{}, ErrBaselineReviewResolution
	}
	if event.SessionID != session.ID || event.Sequence != 0 {
		return domain.AuditEvent{}, ErrEventSequenceConflict
	}
	repository.eventAppendMu.Lock()
	defer repository.eventAppendMu.Unlock()

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("begin baseline review resolution transaction: %w", err)
	}
	defer tx.Rollback()
	resolutionResult, err := tx.ExecContext(ctx, `
		UPDATE session_baseline_reviews
		SET resolution = ?, resolved_utc = ?
		WHERE session_id = ? AND resolution IS NULL
	`, resolution, formatTimestamp(at), session.ID)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("resolve baseline review: %w", err)
	}
	resolutionRows, err := resolutionResult.RowsAffected()
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("read baseline review resolution count: %w", err)
	}
	if resolutionRows != 1 {
		var reviewExists int
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM session_baseline_reviews WHERE session_id = ?)", session.ID).Scan(&reviewExists); err != nil {
			return domain.AuditEvent{}, fmt.Errorf("check baseline review: %w", err)
		}
		if reviewExists == 0 {
			return domain.AuditEvent{}, ErrBaselineReviewNotFound
		}
		return domain.AuditEvent{}, ErrBaselineReviewResolved
	}
	policyJSON, err := encodeSessionMonitoringPolicy(session)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET name = ?, state = ?, revision = ?, monitoring_level = ?, monitoring_policy = ?, updated_utc = ?
		WHERE id = ? AND revision = ?
	`, session.Name, session.State, session.Revision, session.MonitoringLevel, policyJSON, formatTimestamp(at), session.ID, session.Revision-1)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("update session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("read updated session count: %w", err)
	}
	if rows != 1 {
		return domain.AuditEvent{}, ErrSessionRevisionConflict
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) FROM audit_events WHERE session_id = ?
	`, session.ID).Scan(&event.Sequence); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("read baseline review event sequence: %w", err)
	}
	event.Sequence++
	stored, err := repository.appendEventInTransaction(ctx, tx, event, payload)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("commit baseline review resolution transaction: %w", err)
	}
	return stored, nil
}

func (repository *Repository) GetSession(ctx context.Context, id string) (domain.Session, error) {
	var session domain.Session
	var policyJSON string
	err := repository.db.QueryRowContext(ctx, `
		SELECT id, name, state, revision, monitoring_level, monitoring_policy FROM sessions WHERE id = ?
	`, id).Scan(&session.ID, &session.Name, &session.State, &session.Revision, &session.MonitoringLevel, &policyJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("query session: %w", err)
	}
	if session.MonitoringPolicy, err = decodeSessionMonitoringPolicy(policyJSON); err != nil {
		return domain.Session{}, err
	}
	if err := validateSession(session); err != nil {
		return domain.Session{}, fmt.Errorf("stored session is invalid: %w", err)
	}
	return session, nil
}

func (repository *Repository) FindRecoverableSession(ctx context.Context) (domain.Session, bool, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT id, name, state, revision, monitoring_level, monitoring_policy
		FROM sessions
		WHERE state NOT IN ('completed', 'failed')
		ORDER BY updated_utc DESC
		LIMIT 2
	`)
	if err != nil {
		return domain.Session{}, false, fmt.Errorf("query recoverable session: %w", err)
	}
	defer rows.Close()

	var sessions []domain.Session
	for rows.Next() {
		var session domain.Session
		var policyJSON string
		if err := rows.Scan(&session.ID, &session.Name, &session.State, &session.Revision, &session.MonitoringLevel, &policyJSON); err != nil {
			return domain.Session{}, false, fmt.Errorf("scan recoverable session: %w", err)
		}
		if session.MonitoringPolicy, err = decodeSessionMonitoringPolicy(policyJSON); err != nil {
			return domain.Session{}, false, err
		}
		if err := validateSession(session); err != nil {
			return domain.Session{}, false, fmt.Errorf("stored session is invalid: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return domain.Session{}, false, fmt.Errorf("iterate recoverable sessions: %w", err)
	}
	if len(sessions) == 0 {
		return domain.Session{}, false, nil
	}
	if len(sessions) > 1 {
		return domain.Session{}, false, ErrMultipleRecoverableSessions
	}
	return sessions[0], true, nil
}

func (repository *Repository) LastEventSequence(ctx context.Context, sessionID string) (uint64, error) {
	sequence, _, err := repository.LastEventBoundary(ctx, sessionID)
	return sequence, err
}

func (repository *Repository) LastEventBoundary(ctx context.Context, sessionID string) (uint64, time.Time, error) {
	var exists int
	var sequence uint64
	var observed string
	err := repository.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?),
			COALESCE((SELECT sequence FROM audit_events WHERE session_id = ? ORDER BY sequence DESC LIMIT 1), 0),
			COALESCE((SELECT observed_utc FROM audit_events WHERE session_id = ? ORDER BY sequence DESC LIMIT 1), '')
	`, sessionID, sessionID, sessionID).Scan(&exists, &sequence, &observed)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("query last event boundary: %w", err)
	}
	if exists != 1 {
		return 0, time.Time{}, ErrSessionNotFound
	}
	if observed == "" {
		return sequence, time.Time{}, nil
	}
	observedUTC, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parse last event boundary: %w", err)
	}
	return sequence, observedUTC.UTC(), nil
}

func (repository *Repository) AppendEvent(ctx context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error) {
	repository.eventAppendMu.Lock()
	defer repository.eventAppendMu.Unlock()
	return repository.appendEvent(ctx, event, payload)
}

func (repository *Repository) AppendEventAutoSequence(ctx context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error) {
	if event.Sequence != 0 {
		return domain.AuditEvent{}, fmt.Errorf("%w: automatic event sequence must be zero", ErrEventSequenceConflict)
	}
	repository.eventAppendMu.Lock()
	defer repository.eventAppendMu.Unlock()

	sequence, err := repository.LastEventSequence(ctx, event.SessionID)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	event.Sequence = sequence + 1
	return repository.appendEvent(ctx, event, payload)
}

func (repository *Repository) appendEvent(ctx context.Context, event domain.AuditEvent, payload []byte) (domain.AuditEvent, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("begin event transaction: %w", err)
	}
	defer tx.Rollback()
	stored, err := repository.appendEventInTransaction(ctx, tx, event, payload)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("commit audit event: %w", err)
	}
	return stored, nil
}

func (repository *Repository) UpdateSessionAndAppendEvent(
	ctx context.Context,
	session domain.Session,
	at time.Time,
	event domain.AuditEvent,
	payload []byte,
) (domain.AuditEvent, error) {
	if err := validateSession(session); err != nil {
		return domain.AuditEvent{}, err
	}
	if session.Revision == 0 || session.Revision > math.MaxInt64 {
		return domain.AuditEvent{}, ErrSessionRevisionConflict
	}
	if event.SessionID != session.ID || event.Sequence != 0 {
		return domain.AuditEvent{}, ErrEventSequenceConflict
	}
	repository.eventAppendMu.Lock()
	defer repository.eventAppendMu.Unlock()

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("begin lifecycle transaction: %w", err)
	}
	defer tx.Rollback()
	policyJSON, err := encodeSessionMonitoringPolicy(session)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET name = ?, state = ?, revision = ?, monitoring_level = ?, monitoring_policy = ?, updated_utc = ?
		WHERE id = ? AND revision = ?
	`, session.Name, session.State, session.Revision, session.MonitoringLevel, policyJSON, formatTimestamp(at), session.ID, session.Revision-1)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("update session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("read updated session count: %w", err)
	}
	if rows != 1 {
		return domain.AuditEvent{}, ErrSessionRevisionConflict
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) FROM audit_events WHERE session_id = ?
	`, session.ID).Scan(&event.Sequence); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("read lifecycle event sequence: %w", err)
	}
	event.Sequence++
	stored, err := repository.appendEventInTransaction(ctx, tx, event, payload)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("commit lifecycle transaction: %w", err)
	}
	return stored, nil
}

func (repository *Repository) appendEventInTransaction(
	ctx context.Context,
	tx *sql.Tx,
	event domain.AuditEvent,
	payload []byte,
) (domain.AuditEvent, error) {
	if err := event.Validate(); err != nil {
		return domain.AuditEvent{}, err
	}
	if event.Sequence > math.MaxInt64 {
		return domain.AuditEvent{}, ErrEventSequenceConflict
	}

	var sessionExists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)", event.SessionID).Scan(&sessionExists); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("check event session: %w", err)
	}
	if sessionExists != 1 {
		return domain.AuditEvent{}, ErrSessionNotFound
	}

	checkpoint, err := loadEventChainCheckpoint(ctx, tx, event.SessionID)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	if err := repository.verifyEventChainCheckpoint(event.SessionID, checkpoint); err != nil {
		return domain.AuditEvent{}, err
	}
	if event.Sequence != checkpoint.eventCount+1 {
		return domain.AuditEvent{}, fmt.Errorf("%w: got %d want %d", ErrEventSequenceConflict, event.Sequence, checkpoint.eventCount+1)
	}

	event.PreviousHash = append([]byte(nil), checkpoint.tailHash...)
	associatedData, err := repository.integrity.AssociatedData(event)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	nonce, ciphertext, err := repository.cipher.Encrypt(payload, associatedData)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	event.EncryptedPayload = ciphertext
	event.EventHash, err = repository.integrity.Compute(event, nonce)
	if err != nil {
		return domain.AuditEvent{}, err
	}

	var windowsSessionID any
	if event.WindowsSessionID != nil {
		windowsSessionID = *event.WindowsSessionID
	}
	storedKeys, err := repository.sealEventKeys(event)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_events (
			event_id, session_id, sequence, category, action, severity,
			observed_utc, monotonic_ticks, windows_session_id, user_sid_hash,
			process_key, object_key, source, confidence, payload_nonce,
			payload_ciphertext, previous_hash, event_hash, keys_encrypted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
	`, event.EventID, event.SessionID, event.Sequence, event.Category, event.Action,
		event.Severity, formatTimestamp(event.ObservedUTC), event.MonotonicTicks,
		windowsSessionID, event.UserSIDHash, storedKeys.ProcessKey, storedKeys.ObjectKey,
		event.Source, event.Confidence, nonce, event.EncryptedPayload,
		event.PreviousHash, event.EventHash)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("insert audit event: %w", err)
	}
	nextCheckpoint, err := repository.newEventChainCheckpoint(event.SessionID, event.Sequence, event.EventHash)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE event_chain_checkpoints
		SET event_count = ?, tail_hash = ?
		WHERE session_id = ? AND event_count = ? AND tail_hash = ?
	`, event.Sequence, nextCheckpoint.databaseValue(), event.SessionID, checkpoint.eventCount, checkpoint.databaseValue())
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("update event chain checkpoint: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("read event chain checkpoint update count: %w", err)
	}
	if rows != 1 {
		return domain.AuditEvent{}, ErrEventChain
	}
	return event, nil
}

func (repository *Repository) listEvents(ctx context.Context, sessionID string, selection *eventSelection) ([]EventRecord, error) {
	if _, err := repository.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	snapshot, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin audit event snapshot: %w", err)
	}
	defer snapshot.Rollback()
	checkpoint, err := loadEventChainCheckpoint(ctx, snapshot, sessionID)
	if err != nil {
		return nil, err
	}
	if err := repository.verifyEventChainCheckpoint(sessionID, checkpoint); err != nil {
		return nil, err
	}
	if selection != nil {
		selection.total = checkpoint.eventCount
		selection.from = max(selection.from, 1)
		if selection.to == 0 || selection.to > checkpoint.eventCount {
			selection.to = checkpoint.eventCount
		}
		if selection.to >= selection.from && selection.to-selection.from+1 > MaximumReportRangeEvents {
			return nil, ErrEventRangeTooLarge
		}
	}
	rows, err := snapshot.QueryContext(ctx, `
		SELECT event_id, session_id, sequence, category, action, severity,
			observed_utc, monotonic_ticks, windows_session_id, user_sid_hash,
			process_key, object_key, source, confidence, payload_nonce,
			payload_ciphertext, previous_hash, event_hash, keys_encrypted
		FROM audit_events
		WHERE session_id = ?
		ORDER BY sequence
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	records := make([]EventRecord, 0)
	previousHash := make([]byte, eventHashSize)
	var expectedSequence uint64 = 1
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var event domain.AuditEvent
		var observedUTC string
		var windowsSessionID sql.NullInt64
		var nonce []byte
		var keysEncrypted bool
		if err := rows.Scan(
			&event.EventID, &event.SessionID, &event.Sequence, &event.Category,
			&event.Action, &event.Severity, &observedUTC, &event.MonotonicTicks,
			&windowsSessionID, &event.UserSIDHash, &event.ProcessKey, &event.ObjectKey,
			&event.Source, &event.Confidence, &nonce, &event.EncryptedPayload,
			&event.PreviousHash, &event.EventHash, &keysEncrypted,
		); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		if err := repository.openEventKeys(&event, keysEncrypted); err != nil {
			return nil, err
		}
		event.ObservedUTC, err = time.Parse(time.RFC3339Nano, observedUTC)
		if err != nil {
			return nil, fmt.Errorf("%w: event %s timestamp", ErrEventIntegrity, event.EventID)
		}
		if windowsSessionID.Valid {
			if windowsSessionID.Int64 < 0 || windowsSessionID.Int64 > math.MaxUint32 {
				return nil, fmt.Errorf("%w: event %s Windows session id", ErrEventIntegrity, event.EventID)
			}
			value := uint32(windowsSessionID.Int64)
			event.WindowsSessionID = &value
		}
		if event.Sequence != expectedSequence || !bytes.Equal(event.PreviousHash, previousHash) {
			return nil, fmt.Errorf("%w: event %s sequence %d", ErrEventChain, event.EventID, event.Sequence)
		}
		if err := repository.integrity.Verify(event, nonce); err != nil {
			return nil, fmt.Errorf("%w: event %s: %v", ErrEventIntegrity, event.EventID, err)
		}
		previousHash = append(previousHash[:0], event.EventHash...)
		expectedSequence++
		selected := selection == nil || (event.Sequence >= selection.from && event.Sequence <= selection.to)
		readStatus := selection != nil && selection.findingStatuses != nil && event.Action == "risk_finding_status_changed"
		if !selected && !readStatus {
			continue
		}
		if selection != nil && selected {
			selection.payloadBytes += len(event.EncryptedPayload)
			if selection.payloadBytes > maximumReportRangePayloadBytes {
				return nil, ErrEventRangeTooLarge
			}
		}
		associatedData, err := repository.integrity.AssociatedData(event)
		if err != nil {
			return nil, fmt.Errorf("%w: event %s: %v", ErrEventIntegrity, event.EventID, err)
		}
		payload, err := repository.cipher.Decrypt(nonce, event.EncryptedPayload, associatedData)
		if err != nil {
			return nil, fmt.Errorf("decrypt event %s: %w", event.EventID, err)
		}
		if readStatus {
			var status struct {
				FindingID string `json:"findingId"`
				Status    string `json:"status"`
			}
			if json.Unmarshal(payload, &status) == nil && status.FindingID != "" {
				selection.findingStatuses[status.FindingID] = status.Status
			}
		}
		if selected {
			records = append(records, EventRecord{Event: event, Payload: payload})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	if expectedSequence-1 != checkpoint.eventCount || !bytes.Equal(previousHash, checkpoint.tailHash) {
		return nil, ErrEventChain
	}
	return records, nil
}

func loadEventChainCheckpoint(ctx context.Context, queryer queryRower, sessionID string) (eventChainCheckpoint, error) {
	var checkpoint eventChainCheckpoint
	var stored []byte
	err := queryer.QueryRowContext(ctx, `
		SELECT event_count, tail_hash
		FROM event_chain_checkpoints
		WHERE session_id = ?
	`, sessionID).Scan(&checkpoint.eventCount, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return eventChainCheckpoint{}, ErrEventChain
	}
	if err != nil {
		return eventChainCheckpoint{}, fmt.Errorf("query event chain checkpoint: %w", err)
	}
	if len(stored) != eventHashSize*2 {
		return eventChainCheckpoint{}, ErrEventChain
	}
	checkpoint.tailHash = append([]byte(nil), stored[:eventHashSize]...)
	checkpoint.commitment = append([]byte(nil), stored[eventHashSize:]...)
	return checkpoint, nil
}

func (repository *Repository) initializeEventChainCheckpoints(ctx context.Context) error {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT session_id, event_count, tail_hash
		FROM event_chain_checkpoints
	`)
	if err != nil {
		return fmt.Errorf("query event chain checkpoints: %w", err)
	}
	defer rows.Close()

	type update struct {
		sessionID string
		previous  []byte
		next      []byte
	}
	updates := make([]update, 0)
	for rows.Next() {
		var sessionID string
		var eventCount uint64
		var stored []byte
		if err := rows.Scan(&sessionID, &eventCount, &stored); err != nil {
			return fmt.Errorf("scan event chain checkpoint: %w", err)
		}
		switch len(stored) {
		case eventHashSize:
			checkpoint, err := repository.newEventChainCheckpoint(sessionID, eventCount, stored)
			if err != nil {
				return err
			}
			updates = append(updates, update{sessionID: sessionID, previous: stored, next: checkpoint.databaseValue()})
		case eventHashSize * 2:
			checkpoint := eventChainCheckpoint{
				eventCount: eventCount,
				tailHash:   append([]byte(nil), stored[:eventHashSize]...),
				commitment: append([]byte(nil), stored[eventHashSize:]...),
			}
			if err := repository.verifyEventChainCheckpoint(sessionID, checkpoint); err != nil {
				return err
			}
		default:
			return ErrEventChain
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate event chain checkpoints: %w", err)
	}
	for _, update := range updates {
		result, err := repository.db.ExecContext(ctx, `
			UPDATE event_chain_checkpoints
			SET tail_hash = ?
			WHERE session_id = ? AND tail_hash = ?
		`, update.next, update.sessionID, update.previous)
		if err != nil {
			return fmt.Errorf("authenticate event chain checkpoint: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read authenticated event chain checkpoint count: %w", err)
		}
		if count != 1 {
			return ErrEventChain
		}
	}
	return nil
}

func (repository *Repository) newEventChainCheckpoint(sessionID string, eventCount uint64, tailHash []byte) (eventChainCheckpoint, error) {
	if len(tailHash) != eventHashSize {
		return eventChainCheckpoint{}, ErrEventChain
	}
	checkpoint := eventChainCheckpoint{
		eventCount: eventCount,
		tailHash:   append([]byte(nil), tailHash...),
	}
	commitment, err := repository.integrity.ComputeChainCheckpoint(sessionID, eventCount, checkpoint.tailHash)
	if err != nil {
		return eventChainCheckpoint{}, err
	}
	checkpoint.commitment = commitment
	return checkpoint, nil
}

func (repository *Repository) verifyEventChainCheckpoint(sessionID string, checkpoint eventChainCheckpoint) error {
	if err := repository.integrity.VerifyChainCheckpoint(sessionID, checkpoint.eventCount, checkpoint.tailHash, checkpoint.commitment); err != nil {
		return fmt.Errorf("%w: %v", ErrEventIntegrity, err)
	}
	return nil
}

func (checkpoint eventChainCheckpoint) databaseValue() []byte {
	value := make([]byte, 0, eventHashSize*2)
	value = append(value, checkpoint.tailHash...)
	return append(value, checkpoint.commitment...)
}

func validateSession(session domain.Session) error {
	policy, err := monitoringPolicyForSession(session)
	if err != nil {
		return err
	}
	validated, err := domain.NewSessionWithMonitoringPolicy(session.ID, session.Name, policy)
	if err != nil {
		return err
	}
	if validated.ID != session.ID || validated.Name != session.Name || validated.MonitoringLevel != session.MonitoringLevel {
		return errors.New("session id and name must not contain surrounding whitespace")
	}
	switch session.State {
	case domain.SessionStateDraft, domain.SessionStatePreparing, domain.SessionStateBaselineReview, domain.SessionStateActive,
		domain.SessionStateDegraded, domain.SessionStatePaused, domain.SessionStateFinalizing,
		domain.SessionStateCompleted, domain.SessionStateFailed:
		return nil
	default:
		return errors.New("invalid session state")
	}
}

func monitoringPolicyForSession(session domain.Session) (domain.MonitoringPolicy, error) {
	if err := (domain.MonitoringProfile{Level: session.MonitoringLevel}).Validate(); err != nil {
		return domain.MonitoringPolicy{}, err
	}
	policy := session.MonitoringPolicy
	if policy == (domain.MonitoringPolicy{}) {
		policy = domain.DefaultMonitoringPolicy()
		policy.StrictReadAuditEnabled = session.MonitoringLevel == domain.MonitoringLevelStrict
	}
	policy = policy.Resolved()
	if err := policy.Validate(); err != nil {
		return domain.MonitoringPolicy{}, err
	}
	if policy.MonitoringLevel() != session.MonitoringLevel {
		return domain.MonitoringPolicy{}, errors.New("session monitoring level does not match policy")
	}
	return policy, nil
}

func encodeSessionMonitoringPolicy(session domain.Session) (string, error) {
	policy, err := monitoringPolicyForSession(session)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode session monitoring policy: %w", err)
	}
	return string(encoded), nil
}

func decodeSessionMonitoringPolicy(encoded string) (domain.MonitoringPolicy, error) {
	var policy domain.MonitoringPolicy
	if err := json.Unmarshal([]byte(encoded), &policy); err != nil {
		return domain.MonitoringPolicy{}, fmt.Errorf("decode session monitoring policy: %w", err)
	}
	policy = policy.Resolved()
	if err := policy.Validate(); err != nil {
		return domain.MonitoringPolicy{}, fmt.Errorf("validate session monitoring policy: %w", err)
	}
	return policy, nil
}

func (resolution BaselineReviewResolution) valid() bool {
	return resolution == BaselineReviewResolutionContinue || resolution == BaselineReviewResolutionCancel
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
