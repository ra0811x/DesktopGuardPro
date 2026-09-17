package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const defaultDeviceSnapshotInterval = 5 * time.Second

type DeviceInfo struct {
	InstanceID   string `json:"instanceId"`
	Class        string `json:"class,omitempty"`
	Name         string `json:"name,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	SerialNumber string `json:"serialNumber,omitempty"`
	VolumeLabels string `json:"volumeLabels,omitempty"`
	DriveLetters string `json:"driveLetters,omitempty"`
}

func (device DeviceInfo) Key() string { return strings.ToLower(device.InstanceID) }

type deviceSnapshot func() ([]DeviceInfo, error)

type DeviceCollector struct {
	interval         time.Duration
	snapshot         deviceSnapshot
	now              func() time.Time
	recordConnect    bool
	recordDisconnect bool
}

type DeviceCollectorOptions struct {
	Interval         time.Duration
	RecordConnect    bool
	RecordDisconnect bool
}

func NewDeviceCollector(interval time.Duration) (*DeviceCollector, error) {
	return newDeviceCollector(interval, snapshotWindowsDevices, time.Now)
}

func NewDeviceCollectorWithOptions(options DeviceCollectorOptions) (*DeviceCollector, error) {
	return newDeviceCollectorWithOptions(options, snapshotWindowsDevices, time.Now)
}

func newDeviceCollector(interval time.Duration, snapshot deviceSnapshot, now func() time.Time) (*DeviceCollector, error) {
	return newDeviceCollectorWithOptions(DeviceCollectorOptions{
		Interval: interval, RecordConnect: true, RecordDisconnect: true,
	}, snapshot, now)
}

func newDeviceCollectorWithOptions(options DeviceCollectorOptions, snapshot deviceSnapshot, now func() time.Time) (*DeviceCollector, error) {
	interval := options.Interval
	if interval == 0 {
		interval = defaultDeviceSnapshotInterval
	}
	if interval < 0 || snapshot == nil || now == nil {
		return nil, errors.New("invalid device collector configuration")
	}
	return &DeviceCollector{
		interval: interval, snapshot: snapshot, now: now,
		recordConnect: options.RecordConnect, recordDisconnect: options.RecordDisconnect,
	}, nil
}

func (*DeviceCollector) Name() string { return "windows_device_snapshot" }

func (collector *DeviceCollector) Run(ctx context.Context, sink Sink) error {
	previous, err := collector.snapshot()
	if err != nil {
		return fmt.Errorf("capture initial device snapshot: %w", err)
	}
	previousByKey := indexDevices(previous)
	knownByKey := indexDevices(previous)
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := collector.snapshot()
			if err != nil {
				return fmt.Errorf("capture device snapshot: %w", err)
			}
			currentByKey := indexDevices(current)
			connected, disconnected := diffDevices(previousByKey, currentByKey)
			observedUTC := collector.now().UTC()
			if collector.recordDisconnect {
				for _, device := range disconnected {
					if err := collector.emit(ctx, sink, "device_temporarily_removed", device, observedUTC); err != nil {
						return err
					}
				}
			}
			if collector.recordConnect {
				for _, device := range connected {
					action := "device_connected"
					if _, known := knownByKey[device.Key()]; known {
						action = "device_reconnected"
					}
					if err := collector.emit(ctx, sink, action, device, observedUTC); err != nil {
						return err
					}
				}
			}
			for _, device := range connected {
				knownByKey[device.Key()] = device
			}
			previousByKey = currentByKey
		}
	}
}

func (collector *DeviceCollector) emit(ctx context.Context, sink Sink, action string, device DeviceInfo, observedUTC time.Time) error {
	payload, err := json.Marshal(device)
	if err != nil {
		return fmt.Errorf("encode device observation: %w", err)
	}
	err = sink.Emit(ctx, Observation{
		Category:    domain.EventCategoryDevice,
		Action:      action,
		Severity:    domain.EventSeverityMedium,
		ObservedUTC: observedUTC,
		ObjectKey:   device.InstanceID,
		Source:      collector.Name(),
		Confidence:  domain.EventConfidenceSnapshotDiff,
		Payload:     payload,
	})
	if err != nil && !errors.Is(err, ErrObservationQueueFull) {
		return fmt.Errorf("emit device observation: %w", err)
	}
	return nil
}

func indexDevices(devices []DeviceInfo) map[string]DeviceInfo {
	indexed := make(map[string]DeviceInfo, len(devices))
	for _, device := range devices {
		if device.InstanceID != "" {
			indexed[device.Key()] = device
		}
	}
	return indexed
}

func diffDevices(previous, current map[string]DeviceInfo) (connected, disconnected []DeviceInfo) {
	for key, device := range current {
		if _, exists := previous[key]; !exists {
			connected = append(connected, device)
		}
	}
	for key, device := range previous {
		if _, exists := current[key]; !exists {
			disconnected = append(disconnected, device)
		}
	}
	sort.Slice(connected, func(left, right int) bool { return connected[left].Key() < connected[right].Key() })
	sort.Slice(disconnected, func(left, right int) bool { return disconnected[left].Key() < disconnected[right].Key() })
	return connected, disconnected
}
