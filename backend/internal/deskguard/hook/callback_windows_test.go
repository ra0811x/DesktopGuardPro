package hook

import (
	"fmt"
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
		{"unlocking_without_password_focus", wmKeyDown, 0, stateUnlocking, true, nil},
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

func TestMouseCallbackBlocksEverySourceWhileUnlockPromptIsOpen(t *testing.T) {
	for _, flags := range []uint32{0, llmhfInjected} {
		t.Run(fmt.Sprintf("flags_%d", flags), func(t *testing.T) {
			engine := useCallbackEngine(t)
			engine.BeginUnlock()
			input := &msllhookstruct{Pt: point{X: 10, Y: 20}, Flags: flags}
			result, _, _ := syscall.SyscallN(mouseCallback, hcAction, wmLButtonDown, uintptr(unsafe.Pointer(input)))
			runtime.KeepAlive(input)
			if result != 1 {
				t.Fatal("unlock prompt allowed mouse input outside the password field")
			}
			checkCallbackEvent(t, engine, nil)
		})
	}
}

func TestUnlockPromptOnlyAllowsPasswordFieldKeyboardInput(t *testing.T) {
	engine := useCallbackEngine(t)
	engine.BeginUnlock()
	engine.targetMu.Lock()
	engine.unlockDialog = 1
	engine.unlockInput = 2
	engine.unlockInputActive = func(dialog, input uintptr) bool { return dialog == 1 && input == 2 }
	engine.targetMu.Unlock()
	for _, test := range []struct {
		name    string
		vk      uint32
		flags   uint32
		blocked bool
	}{
		{"letter", 'A', 0, false},
		{"injected_letter", 'A', llkhfInjected, false},
		{"backspace", 0x08, 0, false},
		{"enter", 0x0D, 0, false},
		{"escape_relocks", 0x1B, 0, false},
		{"tab", 0x09, 0, true},
		{"alt", vkMenu, 0, true},
		{"windows_key", 0x5B, 0, true},
		{"function_key", 0x73, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := &kbdllhookstruct{VkCode: test.vk, Flags: test.flags}
			result, _, _ := syscall.SyscallN(keyboardCallback, hcAction, wmKeyDown, uintptr(unsafe.Pointer(input)))
			runtime.KeepAlive(input)
			if (result != 0) != test.blocked {
				t.Fatalf("callback result = %d, want blocked=%t", result, test.blocked)
			}
			checkCallbackEvent(t, engine, nil)
		})
	}
	engine.targetMu.Lock()
	engine.unlockInputActive = func(uintptr, uintptr) bool { return false }
	engine.targetMu.Unlock()
	input := &kbdllhookstruct{VkCode: 'A'}
	result, _, _ := syscall.SyscallN(keyboardCallback, hcAction, wmKeyDown, uintptr(unsafe.Pointer(input)))
	runtime.KeepAlive(input)
	if result != 1 {
		t.Fatal("inactive password field received keyboard input")
	}
}

func TestSystemCredentialUnlockKeepsNativePromptInputAvailable(t *testing.T) {
	engine := useCallbackEngine(t)
	engine.SetUnlockInputRestricted(false)
	engine.BeginUnlock()
	keyboard := &kbdllhookstruct{VkCode: 'A'}
	keyboardResult, _, _ := syscall.SyscallN(keyboardCallback, hcAction, wmKeyDown, uintptr(unsafe.Pointer(keyboard)))
	runtime.KeepAlive(keyboard)
	mouse := &msllhookstruct{Pt: point{X: 10, Y: 20}}
	mouseResult, _, _ := syscall.SyscallN(mouseCallback, hcAction, wmLButtonDown, uintptr(unsafe.Pointer(mouse)))
	runtime.KeepAlive(mouse)
	if keyboardResult != 0 || mouseResult != 0 {
		t.Fatal("system credential prompt input was blocked")
	}
}

func TestKeyboardCallbackUnlocksThroughNativePointer(t *testing.T) {
	engine := useCallbackEngine(t)
	engine.SetHotkey(Hotkey{Ctrl: true, Alt: true, VK: 'U'})
	for _, key := range []uint32{vkControl, vkMenu, 'U'} {
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
	if engine.State() != stateUnlocking {
		t.Fatalf("hotkey state = %d, want unlocking", engine.State())
	}
	input := &kbdllhookstruct{VkCode: 'U'}
	syscall.SyscallN(keyboardCallback, hcAction, wmKeyDown, uintptr(unsafe.Pointer(input)))
	runtime.KeepAlive(input)
	select {
	case <-engine.UnlockRequests():
		t.Fatal("repeated hotkey requested a second unlock prompt")
	default:
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
