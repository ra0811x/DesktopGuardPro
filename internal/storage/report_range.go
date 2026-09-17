package storage

import (
	"context"
	"errors"
)

const MaximumReportRangeEvents = 100_000
const maximumReportRangePayloadBytes = 32 * 1024 * 1024

var ErrEventRangeTooLarge = errors.New("event range exceeds report capacity")

type eventSelection struct {
	from, to, total uint64
	payloadBytes    int
}

func (repository *Repository) ListEvents(ctx context.Context, sessionID string) ([]EventRecord, error) {
	return repository.listEvents(ctx, sessionID, nil)
}

// Verify the whole snapshot's chain, but retain and decrypt only the selected
// report range. The returned total comes from that same authenticated snapshot.
func (repository *Repository) ListEventsRange(ctx context.Context, sessionID string, from, to uint64) ([]EventRecord, uint64, error) {
	if to > 0 && from > to {
		return nil, 0, ErrTimelineQueryInvalid
	}
	selection := &eventSelection{from: from, to: to}
	records, err := repository.listEvents(ctx, sessionID, selection)
	return records, selection.total, err
}
