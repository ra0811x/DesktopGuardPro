package agent

// Service-facing names for the DeskGuard engine states and audit categories.
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

type BlockedInputEvent struct {
	Kind                             shieldInputKind
	KeyCode                          int
	X, Y                             int32
	Injected, LowerIntegrity, Record bool
}
