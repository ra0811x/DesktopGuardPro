package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

type RegistryValue struct {
	KeyPath   string `json:"keyPath"`
	ValueName string `json:"valueName"`
	ValueType uint32 `json:"valueType"`
	Data      []byte `json:"data,omitempty"`
}

type registrySnapshot func() (map[string]RegistryValue, error)
type registryChangeWait func(context.Context) error

const registryReconciliationInterval = time.Second

type RegistryCollector struct {
	name       string
	category   domain.EventCategory
	severity   domain.EventSeverity
	snapshot   registrySnapshot
	waitChange registryChangeWait
	now        func() time.Time
}

func newRegistryCollector(
	name string,
	category domain.EventCategory,
	severity domain.EventSeverity,
	snapshot registrySnapshot,
	waitChange registryChangeWait,
	now func() time.Time,
) (*RegistryCollector, error) {
	name = strings.TrimSpace(name)
	if name == "" || snapshot == nil || waitChange == nil || now == nil {
		return nil, errors.New("registry collector dependency is required")
	}
	if category != domain.EventCategorySoftware && category != domain.EventCategorySystem {
		return nil, errors.New("registry collector category must be software or system")
	}
	return &RegistryCollector{
		name: name, category: category, severity: severity,
		snapshot: snapshot, waitChange: waitChange, now: now,
	}, nil
}

func (collector *RegistryCollector) Name() string { return collector.name }

func (collector *RegistryCollector) Run(ctx context.Context, sink Sink) error {
	previous, err := collector.snapshot()
	if err != nil {
		return fmt.Errorf("capture initial registry snapshot: %w", err)
	}
	for {
		waitContext, cancel := context.WithTimeout(ctx, registryReconciliationInterval)
		waitErr := collector.waitChange(waitContext)
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
			return waitErr
		}
		// A bounded reconciliation also covers changes made before subscription
		// or while a previous batch was being emitted, even if no new signal comes.
		current, err := collector.snapshot()
		if err != nil {
			return fmt.Errorf("capture registry snapshot: %w", err)
		}
		changes := diffRegistryValues(previous, current)
		for _, change := range changes {
			if err := collector.emit(ctx, sink, change); err != nil {
				return err
			}
		}
		previous = current
	}
}

type registryDifference struct {
	action string
	before *RegistryValue
	after  *RegistryValue
}

func diffRegistryValues(previous, current map[string]RegistryValue) []registryDifference {
	changes := make([]registryDifference, 0)
	for identity, value := range current {
		old, exists := previous[identity]
		if !exists {
			currentValue := value
			changes = append(changes, registryDifference{action: "registry_value_added", after: &currentValue})
		} else if old.ValueType != value.ValueType || !bytes.Equal(old.Data, value.Data) {
			previousValue, currentValue := old, value
			changes = append(changes, registryDifference{action: "registry_value_modified", before: &previousValue, after: &currentValue})
		}
	}
	for identity, value := range previous {
		if _, exists := current[identity]; !exists {
			previousValue := value
			changes = append(changes, registryDifference{action: "registry_value_removed", before: &previousValue})
		}
	}
	sort.Slice(changes, func(left, right int) bool {
		leftValue, rightValue := registryDifferenceValue(changes[left]), registryDifferenceValue(changes[right])
		leftKey := leftValue.KeyPath + "\x00" + leftValue.ValueName + "\x00" + changes[left].action
		rightKey := rightValue.KeyPath + "\x00" + rightValue.ValueName + "\x00" + changes[right].action
		return leftKey < rightKey
	})
	return changes
}

func registryDifferenceValue(change registryDifference) RegistryValue {
	if change.after != nil {
		return *change.after
	}
	if change.before != nil {
		return *change.before
	}
	return RegistryValue{}
}

func (collector *RegistryCollector) emit(ctx context.Context, sink Sink, change registryDifference) error {
	value := registryDifferenceValue(change)
	payload, err := json.Marshal(struct {
		Before *RegistryValue `json:"before,omitempty"`
		After  *RegistryValue `json:"after,omitempty"`
		Source string         `json:"source"`
	}{Before: change.before, After: change.after, Source: collector.name})
	if err != nil {
		return fmt.Errorf("encode registry observation: %w", err)
	}
	objectKey := value.KeyPath
	if value.ValueName != "" {
		objectKey += "\\" + value.ValueName
	}
	err = sink.Emit(ctx, Observation{
		Category:    collector.category,
		Action:      change.action,
		Severity:    collector.severity,
		ObservedUTC: collector.now().UTC(),
		ObjectKey:   objectKey,
		Source:      collector.name,
		Confidence:  domain.EventConfidenceSnapshotDiff,
		Payload:     payload,
	})
	if err != nil && !errors.Is(err, ErrObservationQueueFull) {
		return fmt.Errorf("emit registry observation: %w", err)
	}
	return nil
}
