package agent

import (
	"testing"
	"time"
)

func TestInputDevicePathMetadata(t *testing.T) {
	path := `\\?\HID#VID_046D&PID_C077&MI_00#7&1234&0&0000#{4d1e55b2-f16f-11cf-88cb-001111000030}`
	if got, want := inputDeviceInstanceID(path), `HID\VID_046D&PID_C077&MI_00\7&1234&0&0000`; got != want {
		t.Fatalf("inputDeviceInstanceID() = %q, want %q", got, want)
	}
	vendor, product := inputDeviceUSBIDs(path)
	if vendor != "046D" || product != "C077" {
		t.Fatalf("inputDeviceUSBIDs() = %q, %q", vendor, product)
	}
	if vendor, product := inputDeviceUSBIDs(`HID#VID_BAD!&PID_12`); vendor != "" || product != "" {
		t.Fatalf("invalid IDs = %q, %q", vendor, product)
	}
}

func TestInputDeviceUnknownTypeIsNotClassifiedAsMouse(t *testing.T) {
	if kind := inputDeviceKind(inputDeviceTypeUnknown); kind != "" {
		t.Fatalf("inputDeviceKind(unknown) = %q", kind)
	}
}

func TestInputDeviceTrackerMarksLatestActiveDevice(t *testing.T) {
	tracker := newInputDeviceTracker()
	keyboard := InputDeviceIdentity{Kind: "keyboard", Handle: 10, InstanceID: `HID\KEYBOARD`}
	mouse := InputDeviceIdentity{Kind: "mouse", Handle: 20, InstanceID: `HID\MOUSE`}
	tracker.replaceKnown([]InputDeviceIdentity{keyboard, mouse})
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

	tracker.mu.Lock()
	tracker.activeKB = keyboard.Handle
	tracker.activeMouse = mouse.Handle
	tracker.lastActive[keyboard.Handle] = now
	tracker.mu.Unlock()

	tracker.mu.RLock()
	if tracker.activeKB != 10 || tracker.activeMouse != 20 || !tracker.lastActive[10].Equal(now) {
		t.Fatalf("active devices = keyboard %d, mouse %d, last active %v", tracker.activeKB, tracker.activeMouse, tracker.lastActive)
	}
	tracker.mu.RUnlock()
}

func TestInputDeviceTrackerPreservesIdentityForRemoval(t *testing.T) {
	tracker := newInputDeviceTracker()
	device := InputDeviceIdentity{Kind: "keyboard", Handle: 42, InstanceID: `HID\REMOVED`}
	tracker.replaceKnown([]InputDeviceIdentity{device})
	tracker.recordDeviceChange(inputDeviceChangeRemoval, device.Handle)

	select {
	case change := <-tracker.Events():
		if change.Action != "removed" || change.Device.InstanceID != device.InstanceID {
			t.Fatalf("change = %+v", change)
		}
	default:
		t.Fatal("removal event was not published")
	}
}
