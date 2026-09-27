package hook

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Test through the Windows callback ABI without installing system hooks.
func useCallbackEngine(t *testing.T) *Engine {
	t.Helper()
	activeMu.Lock()
	previous := activeEngine
	engine := New(Hotkey{})
	engine.Protect()
	activeEngine = engine
	t.Cleanup(func() {
		activeEngine = previous
		activeMu.Unlock()
	})
	return engine
}

func checkCallbackEvent(t *testing.T, engine *Engine, want *Event) {
	t.Helper()
	select {
	case got := <-engine.Events():
		if want == nil || got != *want {
			t.Fatalf("callback event = %+v, want %+v", got, want)
		}
	default:
		if want != nil {
			t.Fatalf("callback did not report %+v", *want)
		}
	}
}

func TestKeyboardCallbackReceivesNativePointer(t *testing.T) {
	for _, test := range []struct {
		name    string
		message uintptr
		flags   uint32
		state   int32
		blocked bool
		event   *Event
	}{
		{"physical_down", wmKeyDown, 0, stateProtecting, true, &Event{Type: EventKeyboard, VK: 'A'}},
		{"physical_up", wmKeyUp, 0, stateProtecting, true, nil},
		{"injected_down", wmKeyDown, llkhfInjected, stateProtecting, false, nil},
		{"protection_off", wmKeyDown, 0, stateOff, false, nil},
		{"unlocking", wmKeyDown, 0, stateUnlocking, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := useCallbackEngine(t)
			if test.state == stateOff {
				engine.Unprotect()
			} else if test.state == stateUnlocking {
				engine.BeginUnlock()
			}
			input := &kbdllhookstruct{VkCode: 'A', Flags: test.flags}
			result, _, _ := syscall.SyscallN(keyboardCallback, hcAction, test.message, uintptr(unsafe.Pointer(input)))
			runtime.KeepAlive(input)
			if (result != 0) != test.blocked {
				t.Fatalf("callback result = %d, want blocked=%t", result, test.blocked)
			}
			checkCallbackEvent(t, engine, test.event)
		})
	}
}

func TestMouseCallbackReceivesNativePointer(t *testing.T) {
	for _, test := range []struct {
		name    string
		message uintptr
		flags   uint32
		blocked bool
		event   *Event
	}{
		{"physical_click", wmRButtonDown, 0, true, &Event{Type: EventMouse, Button: MouseRight, X: -100, Y: 200}},
		{"physical_move", wmMouseMove, 0, true, nil},
		{"physical_wheel", wmMouseWheel, 0, true, &Event{Type: EventMouse, Button: MouseWheel, X: -100, Y: 200}},
		{"injected_click", wmRButtonDown, llmhfInjected, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := useCallbackEngine(t)
			input := &msllhookstruct{Pt: point{X: -100, Y: 200}, Flags: test.flags}
			result, _, _ := syscall.SyscallN(mouseCallback, hcAction, test.message, uintptr(unsafe.Pointer(input)))
			runtime.KeepAlive(input)
			if (result != 0) != test.blocked {
				t.Fatalf("callback result = %d, want blocked=%t", result, test.blocked)
			}
			checkCallbackEvent(t, engine, test.event)
		})
	}
}

func TestKeyboardCallbackUnlocksThroughNativePointer(t *testing.T) {
	engine := useCallbackEngine(t)
	engine.SetHotkey(Hotkey{Ctrl: true, Alt: true, VK: ' '})
	for _, key := range []uint32{vkControl, vkMenu, ' '} {
		input := &kbdllhookstruct{VkCode: key}
		result, _, _ := syscall.SyscallN(keyboardCallback, hcAction, wmKeyDown, uintptr(unsafe.Pointer(input)))
		runtime.KeepAlive(input)
		if result != 1 {
			t.Fatalf("key %d was not blocked", key)
		}
	}
	select {
	case <-engine.UnlockRequests():
	default:
		t.Fatal("native hotkey callback did not request unlock")
	}
}

func TestCallbacksForwardWithoutReadingIgnoredPointer(t *testing.T) {
	engine := useCallbackEngine(t)
	for _, callback := range []uintptr{keyboardCallback, mouseCallback} {
		// Non-action notifications must forward even when no input structure exists.
		syscall.SyscallN(callback, ^uintptr(0), 0, 0)
		checkCallbackEvent(t, engine, nil)
		activeEngine = nil
		syscall.SyscallN(callback, hcAction, 0, 0)
		activeEngine = engine
		checkCallbackEvent(t, engine, nil)
	}
}
