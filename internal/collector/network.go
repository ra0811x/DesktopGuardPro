package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"

	"desktopguardpro/internal/domain"
)

type networkSnapshot func() ([]NetworkInterface, error)

type NetworkCollector struct {
	interval time.Duration
	snapshot networkSnapshot
	now      func() time.Time
}

func NewNetworkCollector(interval time.Duration) (*NetworkCollector, error) {
	return newNetworkCollector(interval, snapshotNetworkInterfaces, time.Now)
}

func newNetworkCollector(interval time.Duration, snapshot networkSnapshot, now func() time.Time) (*NetworkCollector, error) {
	if interval <= 0 || snapshot == nil || now == nil {
		return nil, errors.New("invalid network collector configuration")
	}
	return &NetworkCollector{interval: interval, snapshot: snapshot, now: now}, nil
}

func (*NetworkCollector) Name() string { return "windows_network_snapshot" }

func (collector *NetworkCollector) Run(ctx context.Context, sink Sink) error {
	previous, err := collector.snapshot()
	if err != nil {
		return fmt.Errorf("capture initial network snapshot: %w", err)
	}
	previousByIndex := indexNetworkInterfaces(previous)
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := collector.snapshot()
			if err != nil {
				return fmt.Errorf("capture network snapshot: %w", err)
			}
			currentByIndex := indexNetworkInterfaces(current)
			for _, index := range changedNetworkInterfaceIndexes(previousByIndex, currentByIndex) {
				action := "network_interface_changed"
				value, exists := currentByIndex[index]
				if !exists {
					action = "network_interface_removed"
					value = previousByIndex[index]
				} else if _, existed := previousByIndex[index]; !existed {
					action = "network_interface_added"
				}
				payload, marshalErr := json.Marshal(value)
				if marshalErr != nil {
					return fmt.Errorf("encode network observation: %w", marshalErr)
				}
				if err := sink.Emit(ctx, Observation{
					Category: domain.EventCategorySystem, Action: action, Severity: domain.EventSeverityMedium,
					ObservedUTC: collector.now().UTC(), ObjectKey: strconv.Itoa(index), Source: collector.Name(),
					Confidence: domain.EventConfidenceSnapshotDiff, Payload: payload,
				}); err != nil && !errors.Is(err, ErrObservationQueueFull) {
					return fmt.Errorf("emit network observation: %w", err)
				}
			}
			previousByIndex = currentByIndex
		}
	}
}

func indexNetworkInterfaces(interfaces []NetworkInterface) map[int]NetworkInterface {
	indexed := make(map[int]NetworkInterface, len(interfaces))
	for _, item := range interfaces {
		indexed[item.Index] = item
	}
	return indexed
}

func changedNetworkInterfaceIndexes(previous, current map[int]NetworkInterface) []int {
	changed := make([]int, 0)
	for index, value := range current {
		if before, exists := previous[index]; !exists || !reflect.DeepEqual(before, value) {
			changed = append(changed, index)
		}
	}
	for index := range previous {
		if _, exists := current[index]; !exists {
			changed = append(changed, index)
		}
	}
	sort.Ints(changed)
	return changed
}
