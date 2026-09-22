package agent

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"desktopguardpro/internal/domain"
	"golang.org/x/sys/windows"
)

const (
	windowsHookLowLevelKeyboard = 13
	windowsMessageQuit          = 0x0012
	windowsMessageKeyDown       = 0x0100
	windowsMessageKeyUp         = 0x0101
	windowsMessageSystemKeyDown = 0x0104
	windowsMessageSystemKeyUp   = 0x0105
	windowsMessageMouseMove     = 0x0200
	windowsMessageLeftDown      = 0x0201
	windowsMessageRightDown     = 0x0204
	windowsMessageMiddleDown    = 0x0207
	windowsMessageXDown         = 0x020B
	windowsKeyboardInjected     = 0x10
	windowsKeyboardLowerIL      = 0x02
	windowsMouseInjected        = 0x01
	windowsMouseLowerIL         = 0x02
	inputShieldStopTimeout      = 5 * time.Second
)

var (
	inputShieldKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	inputShieldNTDLL             = windows.NewLazySystemDLL("ntdll.dll")
	inputShieldGetModuleHandle   = inputShieldKernel32.NewProc("GetModuleHandleW")
	inputShieldPostThreadMessage = user32.NewProc("PostThreadMessageW")
	inputShieldRtlMoveMemory     = inputShieldNTDLL.NewProc("RtlMoveMemory")
	inputShieldWindowFromPoint   = user32.NewProc("WindowFromPoint")
	inputShieldIsChild           = user32.NewProc("IsChild")
	inputShieldGetAncestor       = user32.NewProc("GetAncestor")
	activeWindowsInputShield     atomic.Pointer[windowsInputShield]
)

type keyboardLowLevelEvent struct {
	VirtualKey uint32
	ScanCode   uint32
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type mouseLowLevelEvent struct {
	Point     struct{ X, Y int32 }
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type BlockedInputEvent struct {
	Kind           shieldInputKind
	KeyCode        int
	X              int32
	Y              int32
	Injected       bool
	LowerIntegrity bool
	Record         bool
}

type windowsInputShield struct {
	controller         *inputShieldController
	events             chan BlockedInputEvent
	unlock             chan struct{}
	dropped            atomic.Uint64
	threadID           atomic.Uint32
	verificationWindow atomic.Uintptr

	mu      sync.Mutex
	running bool
	stopped chan struct{}
}

func newWindowsInputShield() *windowsInputShield {
	return &windowsInputShield{
		controller: newInputShieldController(),
		events:     make(chan BlockedInputEvent, 128),
		unlock:     make(chan struct{}, 1),
	}
}

func (shield *windowsInputShield) Start(policy domain.InputShieldPolicy) error {
	shield.mu.Lock()
	if shield.running {
		shield.mu.Unlock()
		return errors.New("input shield is already running")
	}
	if !activeWindowsInputShield.CompareAndSwap(nil, shield) {
		shield.mu.Unlock()
		return errors.New("another input shield is already running")
	}
	shield.running = true
	shield.stopped = make(chan struct{})
	stopped := shield.stopped
	shield.controller.Protect(policy)
	shield.mu.Unlock()

	started := make(chan error, 1)
	go shield.runHooks(started, stopped)
	if err := <-started; err != nil {
		shield.mu.Lock()
		shield.running = false
		shield.mu.Unlock()
		activeWindowsInputShield.CompareAndSwap(shield, nil)
		shield.controller.MarkDegraded()
		return err
	}
	return nil
}

func (shield *windowsInputShield) Stop() {
	shield.mu.Lock()
	if !shield.running {
		shield.mu.Unlock()
		return
	}
	threadID := shield.threadID.Load()
	stopped := shield.stopped
	shield.mu.Unlock()
	// Stop blocking before waiting for the hook thread. A failed or delayed
	// thread message must not leave the current user without input.
	shield.controller.Disable()
	if threadID != 0 {
		inputShieldPostThreadMessage.Call(uintptr(threadID), windowsMessageQuit, 0, 0)
	}
	select {
	case <-stopped:
		activeWindowsInputShield.CompareAndSwap(shield, nil)
	case <-time.After(inputShieldStopTimeout):
		// Keep this shield registered until its hook thread really exits. That
		// prevents a replacement from installing a second global callback while
		// this one is still alive. Its controller is already disabled, so the
		// remaining callback passes every event through.
	}
	shield.mu.Lock()
	shield.running = false
	shield.mu.Unlock()
}

func (shield *windowsInputShield) Events() <-chan BlockedInputEvent { return shield.events }
func (shield *windowsInputShield) UnlockRequests() <-chan struct{}  { return shield.unlock }
func (shield *windowsInputShield) DroppedEvents() uint64            { return shield.dropped.Load() }
func (shield *windowsInputShield) Healthy() bool {
	shield.mu.Lock()
	running := shield.running
	stopped := shield.stopped
	shield.mu.Unlock()
	if !running || stopped == nil {
		return false
	}
	select {
	case <-stopped:
		return false
	default:
		return true
	}
}
func (shield *windowsInputShield) SetVerificationWindow(window uintptr) {
	shield.verificationWindow.Store(window)
}
func (shield *windowsInputShield) State() inputShieldState { return shield.controller.State() }
func (shield *windowsInputShield) CompleteVerification(success bool) {
	shield.controller.CompleteVerification(success)
}

func (shield *windowsInputShield) runHooks(started chan<- error, stopped chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(stopped)
	defer activeWindowsInputShield.CompareAndSwap(shield, nil)

	threadID := windows.GetCurrentThreadId()
	shield.threadID.Store(threadID)
	module, _, _ := inputShieldGetModuleHandle.Call(0)
	keyboardCallback := syscall.NewCallback(inputShieldKeyboardCallback)
	mouseCallback := syscall.NewCallback(inputShieldMouseCallback)
	keyboardHook, _, keyboardErr := setWindowsHookEx.Call(windowsHookLowLevelKeyboard, keyboardCallback, module, 0)
	if keyboardHook == 0 {
		started <- errors.New("install input shield keyboard hook: " + keyboardErr.Error())
		return
	}
	defer unhookWindowsHookEx.Call(keyboardHook)
	mouseHook, _, mouseErr := setWindowsHookEx.Call(windowsHookLowLevelMouse, mouseCallback, module, 0)
	if mouseHook == 0 {
		started <- errors.New("install input shield mouse hook: " + mouseErr.Error())
		return
	}
	defer unhookWindowsHookEx.Call(mouseHook)
	// Create the message queue before declaring the hook available. Without
	// this, a rapid Stop can post WM_QUIT before the queue exists and wait
	// forever for a thread that never receives it.
	var queued windowsMessage
	peekMessage.Call(uintptr(unsafe.Pointer(&queued)), 0, 0, 0, 0)
	started <- nil

	var message windowsMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
	}
}

func inputShieldKeyboardCallback(code int, message uintptr, data uintptr) uintptr {
	shield := activeWindowsInputShield.Load()
	if shield == nil || code < 0 {
		return inputShieldCallNext(code, message, data)
	}
	eventData := decodeKeyboardLowLevelEvent(data)
	down := message == windowsMessageKeyDown || message == windowsMessageSystemKeyDown
	up := message == windowsMessageKeyUp || message == windowsMessageSystemKeyUp
	if !down && !up {
		return inputShieldCallNext(code, message, data)
	}
	event := shieldInputEvent{
		Kind: shieldInputKeyboard, KeyCode: int(eventData.VirtualKey), Down: down,
		Injected:           eventData.Flags&windowsKeyboardInjected != 0,
		LowerIntegrity:     eventData.Flags&windowsKeyboardLowerIL != 0,
		VerificationTarget: shield.isVerificationKeyboardTarget(),
		TimestampMS:        eventData.Time,
	}
	decision := shield.controller.Decide(event)
	shield.publish(event, decision)
	if decision.Block {
		return 1
	}
	return inputShieldCallNext(code, message, data)
}

func inputShieldMouseCallback(code int, message uintptr, data uintptr) uintptr {
	shield := activeWindowsInputShield.Load()
	if shield == nil || code < 0 {
		return inputShieldCallNext(code, message, data)
	}
	eventData := decodeMouseLowLevelEvent(data)
	event := shieldInputEvent{
		Kind: inputShieldMouseKind(message), X: eventData.Point.X, Y: eventData.Point.Y,
		Injected:           eventData.Flags&windowsMouseInjected != 0,
		LowerIntegrity:     eventData.Flags&windowsMouseLowerIL != 0,
		VerificationTarget: shield.isVerificationMouseTarget(eventData.Point.X, eventData.Point.Y),
		TimestampMS:        eventData.Time,
	}
	decision := shield.controller.Decide(event)
	shield.publish(event, decision)
	if decision.Block {
		return 1
	}
	return inputShieldCallNext(code, message, data)
}

func (shield *windowsInputShield) isVerificationKeyboardTarget() bool {
	target := shield.verificationWindow.Load()
	if target == 0 {
		return false
	}
	foreground := uintptr(windows.GetForegroundWindow())
	return inputShieldWindowBelongsTo(target, foreground)
}

func (shield *windowsInputShield) isVerificationMouseTarget(x, y int32) bool {
	target := shield.verificationWindow.Load()
	if target == 0 {
		return false
	}
	packedPoint := uintptr(uint32(x)) | uintptr(uint64(uint32(y))<<32)
	window, _, _ := inputShieldWindowFromPoint.Call(packedPoint)
	return inputShieldWindowBelongsTo(target, window)
}

func inputShieldWindowBelongsTo(target, candidate uintptr) bool {
	if target == 0 || candidate == 0 {
		return false
	}
	if candidate == target {
		return true
	}
	if child, _, _ := inputShieldIsChild.Call(target, candidate); child != 0 {
		return true
	}
	const getRootOwner = 3
	rootOwner, _, _ := inputShieldGetAncestor.Call(candidate, getRootOwner)
	return rootOwner == target
}

func decodeKeyboardLowLevelEvent(data uintptr) keyboardLowLevelEvent {
	var event keyboardLowLevelEvent
	if data != 0 {
		inputShieldRtlMoveMemory.Call(uintptr(unsafe.Pointer(&event)), data, unsafe.Sizeof(event))
	}
	return event
}

func decodeMouseLowLevelEvent(data uintptr) mouseLowLevelEvent {
	var event mouseLowLevelEvent
	if data != 0 {
		inputShieldRtlMoveMemory.Call(uintptr(unsafe.Pointer(&event)), data, unsafe.Sizeof(event))
	}
	return event
}

func inputShieldMouseKind(message uintptr) shieldInputKind {
	if message == windowsMessageMouseMove {
		return shieldInputMouseMove
	}
	if message == windowsMessageMouseWheel || message == windowsMessageMouseHWheel {
		return shieldInputMouseWheel
	}
	return shieldInputMouseButton
}

func (shield *windowsInputShield) publish(event shieldInputEvent, decision shieldInputDecision) {
	if decision.TriggerUnlock {
		select {
		case shield.unlock <- struct{}{}:
		default:
		}
	}
	if !decision.Notify {
		return
	}
	policy := shield.controller.policy.Load()
	recorded := BlockedInputEvent{
		Kind: event.Kind, Injected: event.Injected, LowerIntegrity: event.LowerIntegrity, Record: decision.Record,
	}
	if policy != nil && policy.RecordKeyNames {
		recorded.KeyCode = event.KeyCode
	}
	if policy != nil && policy.RecordPointerCoordinates {
		recorded.X = event.X
		recorded.Y = event.Y
	}
	select {
	case shield.events <- recorded:
	default:
		shield.dropped.Add(1)
	}
}

func inputShieldCallNext(code int, message uintptr, data uintptr) uintptr {
	result, _, _ := callNextHookEx.Call(0, uintptr(code), message, data)
	return result
}
