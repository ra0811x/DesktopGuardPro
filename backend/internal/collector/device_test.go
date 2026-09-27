package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDiffDevicesIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	previous := indexDevices([]DeviceInfo{{InstanceID: `USB\VID_1234`, Name: "Device"}})
	current := indexDevices([]DeviceInfo{{InstanceID: `usb\vid_1234`, Name: "Device"}})
	connected, disconnected := diffDevices(previous, current)
	if len(connected) != 0 || len(disconnected) != 0 {
		t.Fatalf("case-only change produced connected=%+v disconnected=%+v", connected, disconnected)
	}
}

func TestDeviceCollectorEmitsSnapshotDifferences(t *testing.T) {
	t.Parallel()

	oldDevice := DeviceInfo{InstanceID: `USB\OLD`, Class: "USB", Name: "Old"}
	newDevice := DeviceInfo{InstanceID: `USB\NEW`, Class: "USB", Name: "New"}
	call := 0
	snapshot := func() ([]DeviceInfo, error) {
		call++
		if call == 1 {
			return []DeviceInfo{oldDevice}, nil
		}
		return []DeviceInfo{newDevice}, nil
	}
	collector, err := newDeviceCollector(time.Millisecond, snapshot, func() time.Time {
		return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("newDeviceCollector() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &observationSink{onEmit: func(count int) {
		if count == 2 {
			cancel()
		}
	}}
	if err := collector.Run(ctx, sink); err != context.Canceled {
		t.Fatalf("Run() error = %v, want %v", err, context.Canceled)
	}
	observations := sink.snapshot()
	if len(observations) != 2 || observations[0].Action != "device_temporarily_removed" || observations[1].Action != "device_connected" {
		t.Fatalf("observations = %+v", observations)
	}
	var payload DeviceInfo
	if err := json.Unmarshal(observations[1].Payload, &payload); err != nil {
		t.Fatalf("decode device payload: %v", err)
	}
	if payload != newDevice {
		t.Fatalf("device payload = %+v, want %+v", payload, newDevice)
	}
}

func TestDeviceCollectorClassifiesReconnectAfterTemporaryRemoval(t *testing.T) {
	device := DeviceInfo{InstanceID: `USB\EVIDENCE`, Class: "USB", Name: "Evidence"}
	call := 0
	snapshot := func() ([]DeviceInfo, error) {
		call++
		if call == 2 {
			return nil, nil
		}
		return []DeviceInfo{device}, nil
	}
	collector, err := newDeviceCollector(time.Millisecond, snapshot, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &observationSink{onEmit: func(count int) {
		if count == 2 {
			cancel()
		}
	}}
	if err := collector.Run(ctx, sink); err != context.Canceled {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 2 || observations[0].Action != "device_temporarily_removed" || observations[1].Action != "device_reconnected" {
		t.Fatalf("reconnect observations = %#v", observations)
	}
}

func TestDeviceCollectorOptionsFilterConnectionEvents(t *testing.T) {
	oldDevice := DeviceInfo{InstanceID: `USB\OLD`, Class: "USB", Name: "Old"}
	newDevice := DeviceInfo{InstanceID: `USB\NEW`, Class: "USB", Name: "New"}
	call := 0
	collector, err := newDeviceCollectorWithOptions(DeviceCollectorOptions{
		Interval: time.Millisecond, RecordDisconnect: true,
	}, func() ([]DeviceInfo, error) {
		call++
		if call == 1 {
			return []DeviceInfo{oldDevice}, nil
		}
		return []DeviceInfo{newDevice}, nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	sink := &observationSink{onEmit: func(count int) { cancel() }}
	if err := collector.Run(ctx, sink); err != context.Canceled {
		t.Fatalf("Run() error = %v", err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "device_temporarily_removed" {
		t.Fatalf("filtered observations = %#v", observations)
	}
}

func TestWindowsDeviceSnapshotReturnsAuditedDevices(t *testing.T) {
	devices, err := snapshotWindowsDevices()
	if err != nil {
		t.Fatalf("snapshotWindowsDevices() error = %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("snapshotWindowsDevices() returned no audited devices")
	}
}

func TestUSBStorageDetailsEnrichDeviceInventory(t *testing.T) {
	details, err := parseUSBStorageDetails([]byte(`[{"InstanceId":"USBSTOR\\DISK&VEN_EXAMPLE\\SERIAL1","Model":"Example Disk","SerialNumber":"SERIAL1","VolumeLabels":["Evidence"],"DriveLetters":["E:"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	devices := enrichUSBStorageDevices(nil, details)
	if len(devices) != 1 || devices[0].Model != "Example Disk" || devices[0].SerialNumber != "SERIAL1" ||
		devices[0].VolumeLabels != "Evidence" || devices[0].DriveLetters != "E:" {
		t.Fatalf("enriched USB storage = %#v", devices)
	}
}

func TestAuditedDeviceKeepsExternalBusesAndExcludesInternalClassMatches(t *testing.T) {
	for _, instanceID := range []string{`USB\VID_1234`, `USBSTOR\DISK`, `HID\VID_1234`, `BTHENUM\DEVICE`, `SWD\WPDBUSENUM\DEVICE`} {
		if !auditedDevice(instanceID, "") {
			t.Errorf("external device %q was excluded", instanceID)
		}
	}
	for _, class := range []string{"Keyboard", "Mouse", "Net", "Monitor"} {
		if auditedDevice(`ROOT\DEVICE`, class) {
			t.Errorf("internal class match %q was included as an external device", class)
		}
	}
}
