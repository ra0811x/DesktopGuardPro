package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const (
	defaultTimelinePageSize = 100
	maximumTimelinePageSize = 500
	timelineCursorVersion   = 1
	maximumTimelineScanSize = 4096
)

var (
	ErrTimelineQueryInvalid  = errors.New("timeline query is invalid")
	ErrTimelineCursorInvalid = errors.New("timeline cursor is invalid")
)

type TimelineQuery struct {
	SessionID   string
	Cursor      string
	Limit       int
	Categories  []domain.EventCategory
	Severities  []domain.EventSeverity
	UserSIDHash string
	User        string
	Process     string
	Path        string
	FromUTC     *time.Time
	ToUTC       *time.Time
}

type TimelinePage struct {
	IntegrityScope    string        `json:"integrityScope,omitempty"`
	ScannedEvents     int           `json:"scannedEvents"`
	Records           []EventRecord `json:"records"`
	NextCursor        string        `json:"nextCursor,omitempty"`
	HasMore           bool          `json:"hasMore"`
	IntegrityVerified bool          `json:"integrityVerified"`
}

type timelineCursor struct {
	Version      int    `json:"v"`
	SessionID    string `json:"s"`
	LastSequence uint64 `json:"q"`
	QueryHash    string `json:"h"`
}

func (repository *Repository) QueryTimeline(ctx context.Context, query TimelineQuery) (TimelinePage, error) {
	normalized, categoryFilter, queryHash, afterSequence, err := normalizeTimelineQuery(query)
	if err != nil {
		return TimelinePage{}, err
	}
	if _, err := repository.GetSession(ctx, normalized.SessionID); err != nil {
		return TimelinePage{}, err
	}
	// The checkpoint and event rows must come from the same snapshot while
	// collectors continue appending to the session.
	snapshot, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return TimelinePage{}, fmt.Errorf("begin timeline snapshot: %w", err)
	}
	defer snapshot.Rollback()
	checkpoint, err := loadEventChainCheckpoint(ctx, snapshot, normalized.SessionID)
	if err != nil {
		return TimelinePage{}, err
	}
	if err := repository.verifyEventChainCheckpoint(normalized.SessionID, checkpoint); err != nil {
		return TimelinePage{}, err
	}
	if afterSequence > checkpoint.eventCount {
		return TimelinePage{}, ErrTimelineCursorInvalid
	}
	firstSequence := max(uint64(1), afterSequence)

	rows, err := snapshot.QueryContext(ctx, `
		SELECT event_id, session_id, sequence, category, action, severity,
			observed_utc, monotonic_ticks, windows_session_id, user_sid_hash,
			process_key, object_key, source, confidence, payload_nonce,
			payload_ciphertext, previous_hash, event_hash, keys_encrypted
		FROM audit_events
		WHERE session_id = ? AND sequence >= ? AND sequence <= ?
		ORDER BY sequence
		LIMIT ?
	`, normalized.SessionID, firstSequence, checkpoint.eventCount, maximumTimelineScanSize+1)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query timeline events: %w", err)
	}
	defer rows.Close()

	page := TimelinePage{Records: make([]EventRecord, 0, normalized.Limit), IntegrityScope: "page"}
	pageBytes := 0
	previousHash := make([]byte, eventHashSize)
	expectedSequence := firstSequence
	consumedSequence := afterSequence
	stoppedEarly := false
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return TimelinePage{}, err
		}
		event, nonce, err := repository.scanTimelineEvent(rows)
		if err != nil {
			return TimelinePage{}, err
		}
		boundary := afterSequence > 0 && event.Sequence == afterSequence
		if event.Sequence != expectedSequence || (!boundary && !bytes.Equal(event.PreviousHash, previousHash)) {
			return TimelinePage{}, fmt.Errorf("%w: event %s sequence %d", ErrEventChain, event.EventID, event.Sequence)
		}
		if err := repository.integrity.Verify(event, nonce); err != nil {
			return TimelinePage{}, fmt.Errorf("%w: event %s: %v", ErrEventIntegrity, event.EventID, err)
		}
		previousHash = append(previousHash[:0], event.EventHash...)
		expectedSequence++
		if boundary {
			continue
		}
		page.ScannedEvents++

		if !timelineEventMatches(event, normalized, categoryFilter) {
			consumedSequence = event.Sequence
			if page.ScannedEvents >= maximumTimelineScanSize {
				stoppedEarly = true
				break
			}
			continue
		}
		associatedData, err := repository.integrity.AssociatedData(event)
		if err != nil {
			return TimelinePage{}, fmt.Errorf("%w: event %s: %v", ErrEventIntegrity, event.EventID, err)
		}
		payload, err := repository.cipher.Decrypt(nonce, event.EncryptedPayload, associatedData)
		if err != nil {
			return TimelinePage{}, fmt.Errorf("decrypt event %s: %w", event.EventID, err)
		}
		if !timelinePayloadMatchesUser(payload, normalized.User) {
			consumedSequence = event.Sequence
			if page.ScannedEvents >= maximumTimelineScanSize {
				stoppedEarly = true
				break
			}
			continue
		}
		if !timelinePayloadMatchesDetails(event, payload, normalized.Process, normalized.Path) {
			consumedSequence = event.Sequence
			if page.ScannedEvents >= maximumTimelineScanSize {
				stoppedEarly = true
				break
			}
			continue
		}
		record := timelinePreview(event, payload)
		encoded, err := json.Marshal(record)
		if err != nil {
			return TimelinePage{}, err
		}
		if len(page.Records) > 0 && pageBytes+len(encoded)+1 > maximumTimelinePageBytes {
			stoppedEarly = true
			break
		}
		pageBytes += len(encoded) + 1
		page.Records = append(page.Records, record)
		consumedSequence = event.Sequence
		if len(page.Records) >= normalized.Limit || page.ScannedEvents >= maximumTimelineScanSize {
			stoppedEarly = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return TimelinePage{}, fmt.Errorf("iterate timeline events: %w", err)
	}
	if (!stoppedEarly && expectedSequence-1 != checkpoint.eventCount) ||
		(expectedSequence-1 == checkpoint.eventCount && !bytes.Equal(previousHash, checkpoint.tailHash)) {
		return TimelinePage{}, ErrEventChain
	}
	page.IntegrityVerified = true
	page.HasMore = consumedSequence < checkpoint.eventCount
	if page.HasMore {
		page.NextCursor, err = encodeTimelineCursor(timelineCursor{
			Version: timelineCursorVersion, SessionID: normalized.SessionID,
			LastSequence: consumedSequence, QueryHash: queryHash,
		})
		if err != nil {
			return TimelinePage{}, err
		}
	}
	return page, nil
}

func normalizeTimelineQuery(query TimelineQuery) (TimelineQuery, map[domain.EventCategory]struct{}, string, uint64, error) {
	query.SessionID = strings.TrimSpace(query.SessionID)
	if query.SessionID == "" || query.Limit < 0 || query.Limit > maximumTimelinePageSize {
		return TimelineQuery{}, nil, "", 0, ErrTimelineQueryInvalid
	}
	if query.Limit == 0 {
		query.Limit = defaultTimelinePageSize
	}
	if (query.FromUTC != nil && !isTimelineUTC(*query.FromUTC)) || (query.ToUTC != nil && !isTimelineUTC(*query.ToUTC)) ||
		(query.FromUTC != nil && query.ToUTC != nil && query.ToUTC.Before(*query.FromUTC)) {
		return TimelineQuery{}, nil, "", 0, ErrTimelineQueryInvalid
	}
	categoryFilter := make(map[domain.EventCategory]struct{}, len(query.Categories))
	for _, category := range query.Categories {
		if !knownTimelineCategory(category) {
			return TimelineQuery{}, nil, "", 0, ErrTimelineQueryInvalid
		}
		categoryFilter[category] = struct{}{}
	}
	query.Categories = make([]domain.EventCategory, 0, len(categoryFilter))
	for category := range categoryFilter {
		query.Categories = append(query.Categories, category)
	}
	sort.Slice(query.Categories, func(left, right int) bool { return query.Categories[left] < query.Categories[right] })
	severityFilter := make(map[domain.EventSeverity]struct{}, len(query.Severities))
	for _, severity := range query.Severities {
		if !knownTimelineSeverity(severity) {
			return TimelineQuery{}, nil, "", 0, ErrTimelineQueryInvalid
		}
		severityFilter[severity] = struct{}{}
	}
	query.Severities = query.Severities[:0]
	for severity := range severityFilter {
		query.Severities = append(query.Severities, severity)
	}
	sort.Slice(query.Severities, func(left, right int) bool { return query.Severities[left] < query.Severities[right] })
	query.UserSIDHash = strings.ToLower(strings.TrimSpace(query.UserSIDHash))
	if query.UserSIDHash != "" {
		digest, err := hex.DecodeString(query.UserSIDHash)
		if err != nil || len(digest) != sha256.Size {
			return TimelineQuery{}, nil, "", 0, ErrTimelineQueryInvalid
		}
	}
	query.User = strings.ToLower(strings.TrimSpace(query.User))
	if len(query.User) > 256 {
		return TimelineQuery{}, nil, "", 0, ErrTimelineQueryInvalid
	}
	query.Process = strings.ToLower(strings.TrimSpace(query.Process))
	query.Path = strings.ToLower(strings.TrimSpace(query.Path))
	queryHash := timelineQueryHash(query)
	if strings.TrimSpace(query.Cursor) == "" {
		return query, categoryFilter, queryHash, 0, nil
	}
	cursor, err := decodeTimelineCursor(query.Cursor)
	if err != nil || cursor.Version != timelineCursorVersion || cursor.SessionID != query.SessionID ||
		cursor.QueryHash != queryHash || cursor.LastSequence == 0 {
		return TimelineQuery{}, nil, "", 0, ErrTimelineCursorInvalid
	}
	return query, categoryFilter, queryHash, cursor.LastSequence, nil
}

func (repository *Repository) scanTimelineEvent(rows *sql.Rows) (domain.AuditEvent, []byte, error) {
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
		return domain.AuditEvent{}, nil, fmt.Errorf("scan timeline event: %w", err)
	}
	if err := repository.openEventKeys(&event, keysEncrypted); err != nil {
		return domain.AuditEvent{}, nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, observedUTC)
	if err != nil {
		return domain.AuditEvent{}, nil, fmt.Errorf("%w: event %s timestamp", ErrEventIntegrity, event.EventID)
	}
	event.ObservedUTC = parsed
	if windowsSessionID.Valid {
		if windowsSessionID.Int64 < 0 || windowsSessionID.Int64 > math.MaxUint32 {
			return domain.AuditEvent{}, nil, fmt.Errorf("%w: event %s Windows session id", ErrEventIntegrity, event.EventID)
		}
		value := uint32(windowsSessionID.Int64)
		event.WindowsSessionID = &value
	}
	return event, nonce, nil
}

func timelineEventMatches(event domain.AuditEvent, query TimelineQuery, categories map[domain.EventCategory]struct{}) bool {
	if len(categories) > 0 {
		if _, exists := categories[event.Category]; !exists {
			return false
		}
	}
	if len(query.Severities) > 0 {
		matched := false
		for _, severity := range query.Severities {
			matched = matched || event.Severity == severity
		}
		if !matched {
			return false
		}
	}
	if query.UserSIDHash != "" && hex.EncodeToString(event.UserSIDHash) != query.UserSIDHash {
		return false
	}
	if query.FromUTC != nil && event.ObservedUTC.Before(*query.FromUTC) {
		return false
	}
	return query.ToUTC == nil || !event.ObservedUTC.After(*query.ToUTC)
}

func timelinePayloadMatchesDetails(event domain.AuditEvent, payload []byte, processQuery, pathQuery string) bool {
	processMatched := processQuery == "" || strings.Contains(strings.ToLower(event.ProcessKey), processQuery) ||
		(event.Category == domain.EventCategoryProcess && strings.Contains(strings.ToLower(event.ObjectKey), processQuery))
	pathMatched := pathQuery == "" || strings.Contains(strings.ToLower(event.ObjectKey), pathQuery)
	if processMatched && pathMatched {
		return true
	}
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	var inspect func(any)
	inspect = func(current any) {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				normalizedKey := strings.ToLower(key)
				if text, ok := child.(string); ok {
					text = strings.ToLower(text)
					if !processMatched && (strings.Contains(normalizedKey, "process") || strings.Contains(normalizedKey, "image") || normalizedKey == "parentchain") {
						processMatched = strings.Contains(text, processQuery)
					}
					if !pathMatched && (strings.HasSuffix(normalizedKey, "path") || normalizedKey == "objectname" || normalizedKey == "root") {
						pathMatched = strings.Contains(text, pathQuery)
					}
				}
				inspect(child)
			}
		case []any:
			for _, child := range item {
				inspect(child)
			}
		}
	}
	inspect(value)
	return processMatched && pathMatched
}

func timelineQueryHash(query TimelineQuery) string {
	from, to := "-", "-"
	if query.FromUTC != nil {
		from = query.FromUTC.Format(time.RFC3339Nano)
	}
	if query.ToUTC != nil {
		to = query.ToUTC.Format(time.RFC3339Nano)
	}
	categories := make([]string, len(query.Categories))
	for index, category := range query.Categories {
		categories[index] = string(category)
	}
	severities := make([]string, len(query.Severities))
	for index, severity := range query.Severities {
		severities[index] = string(severity)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		query.SessionID, strings.Join(categories, ","), strings.Join(severities, ","), query.UserSIDHash, query.User,
		query.Process, query.Path, from, to,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func timelinePayloadMatchesUser(payload []byte, query string) bool {
	if query == "" {
		return true
	}
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	var matches func(any) bool
	matches = func(current any) bool {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				normalizedKey := strings.ToLower(key)
				if normalizedKey == "username" || normalizedKey == "user" || normalizedKey == "usersid" || normalizedKey == "domainname" {
					if text, ok := child.(string); ok && strings.Contains(strings.ToLower(text), query) {
						return true
					}
				}
				if matches(child) {
					return true
				}
			}
		case []any:
			for _, child := range item {
				if matches(child) {
					return true
				}
			}
		}
		return false
	}
	return matches(value)
}

func knownTimelineSeverity(severity domain.EventSeverity) bool {
	switch severity {
	case domain.EventSeverityLow, domain.EventSeverityMedium, domain.EventSeverityHigh:
		return true
	default:
		return false
	}
}

func encodeTimelineCursor(cursor timelineCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode timeline cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeTimelineCursor(encoded string) (timelineCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return timelineCursor{}, ErrTimelineCursorInvalid
	}
	var cursor timelineCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return timelineCursor{}, ErrTimelineCursorInvalid
	}
	return cursor, nil
}

func knownTimelineCategory(category domain.EventCategory) bool {
	switch category {
	case domain.EventCategoryFile, domain.EventCategoryProcess, domain.EventCategorySoftware,
		domain.EventCategorySystem, domain.EventCategoryDevice, domain.EventCategoryHealth:
		return true
	default:
		return false
	}
}

func isTimelineUTC(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
