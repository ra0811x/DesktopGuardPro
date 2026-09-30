package agent

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"desktopguardpro/internal/deskguard/hook"
	"desktopguardpro/internal/domain"
	"golang.org/x/sys/windows"
)

const windowsMessageQuit = 0x0012

var (
	inputShieldKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	inputShieldGetModuleHandle   = inputShieldKernel32.NewProc("GetModuleHandleW")
	inputShieldPostThreadMessage = user32.NewProc("PostThreadMessageW")
	inputShieldRtlMoveMemory     = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlMoveMemory")
)

// Thin adapter: all physical/injected input decisions and unlock recognition
// run in the verbatim DeskGuard hook.Engine, not a second hook implementation.
type windowsInputShield struct {
	engine  *hook.Engine
	events  chan BlockedInputEvent
	unlock  chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	running atomic.Bool
	dropped atomic.Uint64
	mu      sync.Mutex
	stateMu sync.Mutex
	policy  domain.InputShieldPolicy
}

func newWindowsInputShield() *windowsInputShield {
	return &windowsInputShield{engine: hook.New(hook.Hotkey{}),
		events: make(chan BlockedInputEvent, 128), unlock: make(chan struct{}, 1)}
}
func (shield *windowsInputShield) Start(policy domain.InputShieldPolicy) error {
	shield.mu.Lock()
	defer shield.mu.Unlock()
	if shield.running.Load() {
		return errors.New("input shield is already running")
	}
	shield.policy = policy
	shield.engine.SetHotkey(hook.Hotkey{Ctrl: policy.UnlockRequireControl, Alt: policy.UnlockRequireAlt,
		Shift: policy.UnlockRequireShift, VK: uint32(policy.UnlockKeyCode)})
	shield.engine.SetTapMode(policy.UnlockTrigger == domain.InputShieldUnlockTriggerTap)
	shield.engine.SetTap(uint32(policy.UnlockKeyCode), policy.UnlockTapCount,
		time.Duration(policy.UnlockTapWindowMilliseconds)*time.Millisecond)
	shield.engine.SetUnlockInputRestricted(policy.CredentialMode == domain.InputShieldCredentialLocal)
	if err := shield.engine.Start(); err != nil {
		shield.engine.Stop()
		return err
	}
	shield.stop = make(chan struct{})
	shield.stopped = make(chan struct{})
	shield.running.Store(true)
	shield.engine.Protect()
	go shield.relay()
	return nil
}
func (shield *windowsInputShield) Stop() {
	shield.mu.Lock()
	defer shield.mu.Unlock()
	shield.stateMu.Lock()
	if !shield.running.Swap(false) {
		shield.stateMu.Unlock()
		return
	}
	shield.engine.Unprotect()
	shield.stateMu.Unlock()
	close(shield.stop)
	<-shield.stopped
	shield.engine.Stop()
}
func (shield *windowsInputShield) relay() {
	defer close(shield.stopped)
	for {
		select {
		case <-shield.stop:
			return
		case <-shield.engine.UnlockRequests():
			shield.signalUnlock()
		case event := <-shield.engine.Events():
			recorded := BlockedInputEvent{Kind: shieldInputKeyboard, KeyCode: int(event.VK), X: event.X, Y: event.Y,
				Record: shield.policy.RecordBlockedInputCategory}
			if event.Type == hook.EventMouse {
				recorded.Kind = shieldInputMouseButton
				if event.Button == hook.MouseWheel {
					recorded.Kind = shieldInputMouseWheel
				}
			}
			select {
			case shield.events <- recorded:
			default:
				shield.dropped.Add(1)
			}
		}
	}
}
func (shield *windowsInputShield) RequestUnlock() {
	shield.stateMu.Lock()
	defer shield.stateMu.Unlock()
	if !shield.running.Load() || !shield.engine.BeginUnlock() {
		return
	}
	shield.signalUnlock()
}
func (shield *windowsInputShield) signalUnlock() {
	select {
	case shield.unlock <- struct{}{}:
	default:
	}
}
func (shield *windowsInputShield) CompleteVerification(success bool) {
	shield.stateMu.Lock()
	defer shield.stateMu.Unlock()
	if !shield.running.Load() {
		return
	}
	if success {
		shield.engine.Unprotect()
	} else {
		shield.engine.Protect()
	}
}
func (shield *windowsInputShield) State() inputShieldState {
	return inputShieldState(shield.engine.State())
}
func (shield *windowsInputShield) Healthy() bool                    { return shield.running.Load() }
func (shield *windowsInputShield) Events() <-chan BlockedInputEvent { return shield.events }
func (shield *windowsInputShield) UnlockRequests() <-chan struct{}  { return shield.unlock }
func (shield *windowsInputShield) DroppedEvents() uint64            { return shield.dropped.Load() }
