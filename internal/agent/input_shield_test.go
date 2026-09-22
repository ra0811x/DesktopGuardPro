package agent

import (
	"reflect"
	"testing"
	"time"
	"unsafe"

	"desktopguardpro/internal/domain"
)

func TestInputShieldDecodesWindowsHookData(t *testing.T) {
	keyboard := keyboardLowLevelEvent{VirtualKey: 0x55, ScanCode: 22, Flags: windowsKeyboardInjected, Time: 1234, ExtraInfo: 99}
	decodedKeyboard := decodeKeyboardLowLevelEvent(uintptr(unsafe.Pointer(&keyboard)))
	if !reflect.DeepEqual(decodedKeyboard, keyboard) {
		t.Fatalf("decoded keyboard event = %+v, want %+v", decodedKeyboard, keyboard)
	}

	mouse := mouseLowLevelEvent{MouseData: 1, Flags: windowsMouseLowerIL, Time: 5678, ExtraInfo: 101}
	mouse.Point.X = 120
	mouse.Point.Y = 240
	decodedMouse := decodeMouseLowLevelEvent(uintptr(unsafe.Pointer(&mouse)))
	if !reflect.DeepEqual(decodedMouse, mouse) {
		t.Fatalf("decoded mouse event = %+v, want %+v", decodedMouse, mouse)
	}
}

func TestInputShieldBlocksPhysicalAndClassifiesInjectedInput(t *testing.T) {
	tests := []struct {
		name      string
		mode      domain.InjectedInputMode
		injected  bool
		lower     bool
		wantBlock bool
	}{
		{name: "physical", mode: domain.InjectedInputModeCompatible, wantBlock: true},
		{name: "compatible injected", mode: domain.InjectedInputModeCompatible, injected: true},
		{name: "restricted same integrity", mode: domain.InjectedInputModeRestricted, injected: true},
		{name: "restricted lower integrity", mode: domain.InjectedInputModeRestricted, injected: true, lower: true, wantBlock: true},
		{name: "strict injected", mode: domain.InjectedInputModeStrict, injected: true, wantBlock: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := domain.DefaultMonitoringPolicy().InputShield
			policy.InjectedInputMode = test.mode
			controller := newInputShieldController()
			controller.Protect(policy)
			decision := controller.Decide(shieldInputEvent{
				Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true,
				Injected: test.injected, LowerIntegrity: test.lower,
			})
			if decision.Block != test.wantBlock {
				t.Fatalf("Decide().Block = %v, want %v", decision.Block, test.wantBlock)
			}
		})
	}
}

func TestInputShieldUnlockCombinationEntersVerification(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	controller := newInputShieldController()
	controller.Protect(policy)
	controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x11, Down: true})
	controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x12, Down: true})
	decision := controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x20, Down: true})
	if !decision.Block || !decision.TriggerUnlock || controller.State() != inputShieldVerifying {
		t.Fatalf("decision = %+v, state = %d", decision, controller.State())
	}
	if decision := controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true}); decision.Block {
		t.Fatal("verification grace period must allow physical keyboard input")
	}
}

func TestInputShieldMouseOnlyControlCanTriggerUnlock(t *testing.T) {
	for _, trigger := range []domain.InputShieldUnlockTrigger{
		domain.InputShieldUnlockTriggerCombination, domain.InputShieldUnlockTriggerTap,
	} {
		t.Run(string(trigger), func(t *testing.T) {
			policy := domain.DefaultMonitoringPolicy().InputShield
			policy.BlockPhysicalKeyboard = false
			policy.UnlockTrigger = trigger
			policy.UnlockTapCount = 3
			controller := newInputShieldController()
			controller.Protect(policy)
			if decision := controller.Decide(shieldInputEvent{Kind: shieldInputMouseButton, Down: true}); !decision.Block {
				t.Fatalf("mouse input was not blocked: %+v", decision)
			}
			var decision shieldInputDecision
			if trigger == domain.InputShieldUnlockTriggerCombination {
				controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x11, Down: true})
				controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x12, Down: true})
				decision = controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x20, Down: true})
			} else {
				for index := 0; index < policy.UnlockTapCount; index++ {
					decision = controller.Decide(shieldInputEvent{
						Kind: shieldInputKeyboard, KeyCode: policy.UnlockKeyCode,
						Down: true, TimestampMS: uint32(100 + 100*index),
					})
					controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: policy.UnlockKeyCode, Down: false})
				}
			}
			if decision.Block || !decision.TriggerUnlock || controller.State() != inputShieldVerifying {
				t.Fatalf("mouse-only unlock decision = %+v, state = %d", decision, controller.State())
			}
		})
	}
}

func TestInputShieldVerificationGracePeriodExpiresAfterTwentySeconds(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	controller := newInputShieldController()
	controller.Protect(policy)
	if !controller.BeginVerification() {
		t.Fatal("BeginVerification() = false")
	}

	for _, event := range []shieldInputEvent{
		{Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true},
		{Kind: shieldInputMouseButton, Down: true},
		{Kind: shieldInputMouseMove},
	} {
		if decision := controller.Decide(event); decision.Block {
			t.Fatalf("grace-period event %+v was blocked: %+v", event, decision)
		}
	}

	controller.verificationGraceUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if decision := controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true}); !decision.Block {
		t.Fatalf("input after the 20-second grace period was not blocked: %+v", decision)
	}
	if decision := controller.Decide(shieldInputEvent{
		Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true, VerificationTarget: true,
	}); decision.Block {
		t.Fatalf("verification target after grace period was blocked: %+v", decision)
	}
}

func TestInputShieldFailedVerificationClearsUnlockChordState(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	controller := newInputShieldController()
	controller.Protect(policy)
	controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x11, Down: true})
	controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x12, Down: true})
	if decision := controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x20, Down: true}); !decision.TriggerUnlock {
		t.Fatalf("unlock decision = %+v", decision)
	}
	controller.CompleteVerification(false)

	decision := controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x20, Down: true})
	if decision.TriggerUnlock {
		t.Fatalf("stale modifier state retriggered verification: %+v", decision)
	}
}

func TestInputShieldTapUnlockIgnoresKeyRepeat(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	policy.UnlockTrigger = domain.InputShieldUnlockTriggerTap
	policy.UnlockKeyCode = 0x1B
	policy.UnlockTapCount = 3
	controller := newInputShieldController()
	controller.Protect(policy)

	events := []shieldInputEvent{
		{Kind: shieldInputKeyboard, KeyCode: 0x1B, Down: true, TimestampMS: 100},
		{Kind: shieldInputKeyboard, KeyCode: 0x1B, Down: true, TimestampMS: 150},
		{Kind: shieldInputKeyboard, KeyCode: 0x1B, Down: false, TimestampMS: 200},
		{Kind: shieldInputKeyboard, KeyCode: 0x1B, Down: true, TimestampMS: 300},
		{Kind: shieldInputKeyboard, KeyCode: 0x1B, Down: false, TimestampMS: 350},
		{Kind: shieldInputKeyboard, KeyCode: 0x1B, Down: true, TimestampMS: 400},
	}
	for index, event := range events {
		decision := controller.Decide(event)
		if decision.TriggerUnlock != (index == len(events)-1) {
			t.Fatalf("event %d decision = %+v", index, decision)
		}
	}
}

func TestInputShieldVerificationCompletionUsesConfiguredAction(t *testing.T) {
	tests := []struct {
		name      string
		action    domain.InputShieldUnlockAction
		wantState inputShieldState
	}{
		{name: "suspend", action: domain.InputShieldUnlockActionSuspend, wantState: inputShieldSuspended},
		{name: "end session", action: domain.InputShieldUnlockActionEndSession, wantState: inputShieldDisabled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := domain.DefaultMonitoringPolicy().InputShield
			policy.UnlockAction = test.action
			controller := newInputShieldController()
			controller.Protect(policy)
			if !controller.BeginVerification() {
				t.Fatal("BeginVerification() = false")
			}
			controller.CompleteVerification(true)
			if controller.State() != test.wantState {
				t.Fatalf("state = %d, want %d", controller.State(), test.wantState)
			}
		})
	}
}

func TestInputShieldVerificationOnlyAllowsRegisteredTarget(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	controller := newInputShieldController()
	controller.Protect(policy)
	if !controller.BeginVerification() {
		t.Fatal("BeginVerification() = false")
	}
	controller.verificationGraceUntil.Store(time.Now().Add(-time.Second).UnixNano())

	allowed := controller.Decide(shieldInputEvent{
		Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true, VerificationTarget: true,
	})
	if allowed.Block {
		t.Fatalf("registered verification target decision = %+v", allowed)
	}
	blocked := controller.Decide(shieldInputEvent{
		Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true,
	})
	if !blocked.Block {
		t.Fatalf("unrelated window decision = %+v", blocked)
	}
}

func TestInputShieldWarningNotificationDoesNotRequireAuditDetail(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy().InputShield
	policy.RecordBlockedInputCategory = false
	controller := newInputShieldController()
	controller.Protect(policy)
	decision := controller.Decide(shieldInputEvent{Kind: shieldInputKeyboard, KeyCode: 0x41, Down: true})
	if !decision.Block || !decision.Notify || decision.Record {
		t.Fatalf("keyboard decision = %+v", decision)
	}
	move := controller.Decide(shieldInputEvent{Kind: shieldInputMouseMove})
	if !move.Block || move.Notify || move.Record {
		t.Fatalf("mouse move decision = %+v", move)
	}
}
