package agent

import (
	"sync"
	"sync/atomic"

	"desktopguardpro/internal/domain"
)

type inputShieldState uint32

const (
	inputShieldDisabled inputShieldState = iota
	inputShieldProtecting
	inputShieldVerifying
	inputShieldSuspended
	inputShieldDegraded
)

type shieldInputKind uint8

const (
	shieldInputKeyboard shieldInputKind = iota
	shieldInputMouseButton
	shieldInputMouseMove
	shieldInputMouseWheel
)

type shieldInputEvent struct {
	Kind               shieldInputKind
	KeyCode            int
	Down               bool
	Injected           bool
	LowerIntegrity     bool
	VerificationTarget bool
	TimestampMS        uint32
	X                  int32
	Y                  int32
}

type shieldInputDecision struct {
	Block         bool
	Record        bool
	Notify        bool
	TriggerUnlock bool
}

type inputShieldController struct {
	policy atomic.Pointer[domain.InputShieldPolicy]
	state  atomic.Uint32

	mu            sync.Mutex
	keyDown       [256]bool
	tapCount      int
	lastTapTimeMS uint32
}

func newInputShieldController() *inputShieldController {
	controller := &inputShieldController{}
	controller.state.Store(uint32(inputShieldDisabled))
	return controller
}

func (controller *inputShieldController) Protect(policy domain.InputShieldPolicy) {
	copy := policy
	controller.policy.Store(&copy)
	controller.mu.Lock()
	controller.keyDown = [256]bool{}
	controller.tapCount = 0
	controller.lastTapTimeMS = 0
	controller.mu.Unlock()
	controller.state.Store(uint32(inputShieldProtecting))
}

func (controller *inputShieldController) Disable() {
	controller.state.Store(uint32(inputShieldDisabled))
}

func (controller *inputShieldController) BeginVerification() bool {
	return controller.state.CompareAndSwap(uint32(inputShieldProtecting), uint32(inputShieldVerifying))
}

func (controller *inputShieldController) CompleteVerification(success bool) {
	policy := controller.policy.Load()
	if !success {
		controller.state.CompareAndSwap(uint32(inputShieldVerifying), uint32(inputShieldProtecting))
		return
	}
	if policy != nil && policy.UnlockAction == domain.InputShieldUnlockActionEndSession {
		controller.state.Store(uint32(inputShieldDisabled))
		return
	}
	controller.state.Store(uint32(inputShieldSuspended))
}

func (controller *inputShieldController) Resume() bool {
	return controller.state.CompareAndSwap(uint32(inputShieldSuspended), uint32(inputShieldProtecting))
}

func (controller *inputShieldController) MarkDegraded() {
	controller.state.Store(uint32(inputShieldDegraded))
}

func (controller *inputShieldController) State() inputShieldState {
	return inputShieldState(controller.state.Load())
}

func (controller *inputShieldController) Decide(event shieldInputEvent) shieldInputDecision {
	policy := controller.policy.Load()
	if policy == nil {
		return shieldInputDecision{}
	}
	state := controller.State()
	if state != inputShieldProtecting && state != inputShieldVerifying {
		return shieldInputDecision{}
	}
	if state == inputShieldVerifying && event.VerificationTarget {
		return shieldInputDecision{}
	}
	if !controller.inputEnabled(policy, event) {
		return shieldInputDecision{}
	}
	if event.Injected {
		switch policy.InjectedInputMode {
		case domain.InjectedInputModeCompatible:
			return shieldInputDecision{}
		case domain.InjectedInputModeRestricted:
			if !event.LowerIntegrity {
				return shieldInputDecision{}
			}
		}
	}

	decision := shieldInputDecision{Block: true}
	if state == inputShieldVerifying {
		return decision
	}
	if event.Kind == shieldInputKeyboard && !event.Injected {
		firstDown := controller.updateKeyState(event.KeyCode, event.Down)
		if firstDown && controller.matchesUnlock(policy, event) && controller.BeginVerification() {
			decision.TriggerUnlock = true
		}
	}
	decision.Record = policy.RecordBlockedInputCategory && controller.isRecordable(event)
	decision.Notify = controller.isRecordable(event)
	return decision
}

func (*inputShieldController) inputEnabled(policy *domain.InputShieldPolicy, event shieldInputEvent) bool {
	switch event.Kind {
	case shieldInputKeyboard:
		return policy.BlockPhysicalKeyboard
	case shieldInputMouseMove:
		return policy.BlockPhysicalMouse && policy.BlockPointerMovement
	case shieldInputMouseButton, shieldInputMouseWheel:
		return policy.BlockPhysicalMouse
	default:
		return false
	}
}

func (controller *inputShieldController) updateKeyState(keyCode int, down bool) bool {
	if keyCode < 0 || keyCode >= len(controller.keyDown) {
		return false
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	firstDown := down && !controller.keyDown[keyCode]
	controller.keyDown[keyCode] = down
	return firstDown
}

func (controller *inputShieldController) matchesUnlock(policy *domain.InputShieldPolicy, event shieldInputEvent) bool {
	if event.KeyCode != policy.UnlockKeyCode {
		return false
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if policy.UnlockTrigger == domain.InputShieldUnlockTriggerCombination {
		control := controller.keyDown[0x11] || controller.keyDown[0xA2] || controller.keyDown[0xA3]
		alt := controller.keyDown[0x12] || controller.keyDown[0xA4] || controller.keyDown[0xA5]
		shift := controller.keyDown[0x10] || controller.keyDown[0xA0] || controller.keyDown[0xA1]
		return (!policy.UnlockRequireControl || control) && (!policy.UnlockRequireAlt || alt) &&
			(!policy.UnlockRequireShift || shift)
	}
	if controller.tapCount == 0 || event.TimestampMS-controller.lastTapTimeMS > uint32(policy.UnlockTapWindowMilliseconds) {
		controller.tapCount = 1
	} else {
		controller.tapCount++
	}
	controller.lastTapTimeMS = event.TimestampMS
	if controller.tapCount < policy.UnlockTapCount {
		return false
	}
	controller.tapCount = 0
	controller.lastTapTimeMS = 0
	return true
}

func (*inputShieldController) isRecordable(event shieldInputEvent) bool {
	if event.Kind == shieldInputKeyboard {
		return event.Down
	}
	return event.Kind == shieldInputMouseButton || event.Kind == shieldInputMouseWheel
}
