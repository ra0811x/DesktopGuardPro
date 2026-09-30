// Package hook 实现 Windows 全局低级键鼠钩子。
//
// 核心职责：
//   - 保护开启时，拦截本地物理键鼠输入（返回非零阻断），放行远程注入输入（带 INJECTED 标志）。
//   - 拦截到物理输入时通过 channel 上报事件，供上层弹告警 + 写日志。
//   - 在钩子层识别解锁组合键，命中时通过 channel 通知上层弹密码框。
//
// 低级钩子回调由系统在安装钩子的线程上调用，该线程必须有消息循环，
// 因此用一个 runtime.LockOSThread 锁定的独立 goroutine 跑 GetMessage 循环。
// 回调内只做判断与非阻塞上报，重活（弹窗/写日志）交上层异步处理，
// 避免超过 LowLevelHooksTimeout 被系统静默摘钩。
package hook

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 保护状态。
const (
	stateOff        int32 = iota // 关闭：一切放行
	stateProtecting              // 保护中：拦本地物理输入，放行注入输入
	stateUnlocking               // 解锁中：只向已注册的密码输入框放行安全键盘输入
)

// 解锁模式。
const (
	modeCombo int32 = iota // 组合键触发
	modeTap                // 连点某键触发
)

// Win32 常量。
const (
	whKeyboardLL = 13
	whMouseLL    = 14

	llkhfInjected = 0x10 // KBDLLHOOKSTRUCT.flags 注入标志
	llmhfInjected = 0x01 // MSLLHOOKSTRUCT.flags 注入标志

	hcAction = 0

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
	wmXButtonDown = 0x020B

	wmQuit = 0x0012

	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // Alt
	vkLShift  = 0xA0
	vkRShift  = 0xA1
	vkLCtrl   = 0xA2
	vkRCtrl   = 0xA3
	vkLMenu   = 0xA4
	vkRMenu   = 0xA5
)

// EventType 区分被拦截的物理输入来源。
type EventType int

const (
	EventKeyboard EventType = iota
	EventMouse
)

// MouseButton 表示鼠标动作类型。
type MouseButton int

const (
	MouseNone   MouseButton = iota
	MouseLeft               // 左键
	MouseRight              // 右键
	MouseMiddle             // 中键
	MouseWheel              // 滚轮
	MouseX                  // 侧键
)

// Event 表示一次被拦截的物理输入。
type Event struct {
	Type   EventType
	VK     uint32      // 键盘：虚拟键码
	Button MouseButton // 鼠标：动作类型
	X, Y   int32       // 鼠标：屏幕坐标
}

// Hotkey 解锁组合键描述（与 config.Hotkey 对应，避免包依赖循环这里独立定义）。
type Hotkey struct {
	Ctrl  bool
	Alt   bool
	Shift bool
	VK    uint32
}

type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type point struct {
	X, Y int32
}

type msllhookstruct struct {
	Pt          point
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procSetWindowsHook  = user32.NewProc("SetWindowsHookExW")
	procUnhookWindows   = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx  = user32.NewProc("CallNextHookEx")
	procGetForeground   = user32.NewProc("GetForegroundWindow")
	procGetGUIThread    = user32.NewProc("GetGUIThreadInfo")
	procGetWindowThread = user32.NewProc("GetWindowThreadProcessId")
	procGetMessageW     = user32.NewProc("GetMessageW")
	procPostThreadMsgW  = user32.NewProc("PostThreadMessageW")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentTID   = kernel32.NewProc("GetCurrentThreadId")
)

// activeEngine 指向当前运行的引擎。低级钩子回调是全局函数，
// 通过此指针访问引擎状态。同一时刻只允许一个引擎运行。
var (
	activeEngine *Engine
	activeMu     sync.Mutex
)

// Engine 管理键鼠钩子与保护状态。
type Engine struct {
	state int32 // 原子访问，取值 stateOff/stateProtecting/stateUnlocking
	// 本地密码窗口使用严格输入隔离；系统凭据窗口保留原输入路径。
	restrictUnlockInput int32

	mode int32 // 原子访问：modeCombo / modeTap

	hkMu   sync.RWMutex
	unlock Hotkey

	// 连点解锁参数与状态。
	tapMu     sync.Mutex
	tapKey    uint32
	tapCount  int
	tapWindow time.Duration
	tapTimes  []time.Time

	// 物理修饰键当前状态（仅根据物理事件更新，避免远程注入干扰组合键判断）。
	modMu     sync.Mutex
	ctrlDown  bool
	altDown   bool
	shiftDown bool

	targetMu          sync.RWMutex
	unlockDialog      uintptr
	unlockInput       uintptr
	unlockInputActive func(uintptr, uintptr) bool

	events    chan Event
	unlockReq chan struct{}

	threadID uint32
	kbHook   uintptr
	msHook   uintptr

	started chan error
	stopped chan struct{}
	running int32
}

// New 创建引擎。unlock 为解锁组合键。
func New(unlock Hotkey) *Engine {
	return &Engine{
		unlock:              unlock,
		restrictUnlockInput: 1,
		unlockInputActive:   nativeUnlockInputActive,
		events:              make(chan Event, 8),
		unlockReq:           make(chan struct{}, 1),
		started:             make(chan error, 1),
		stopped:             make(chan struct{}),
	}
}

// SetUnlockInputRestricted 设置验证态是否只允许已注册密码框接收输入。
func (e *Engine) SetUnlockInputRestricted(restricted bool) {
	value := int32(0)
	if restricted {
		value = 1
	}
	atomic.StoreInt32(&e.restrictUnlockInput, value)
}

// Events 返回被拦截物理输入的事件通道。
func (e *Engine) Events() <-chan Event { return e.events }

// UnlockRequests 返回解锁组合键命中的通知通道。
func (e *Engine) UnlockRequests() <-chan struct{} { return e.unlockReq }

// SetHotkey 更新解锁组合键。
func (e *Engine) SetHotkey(h Hotkey) {
	e.hkMu.Lock()
	e.unlock = h
	e.hkMu.Unlock()
}

// SetTapMode 切换解锁模式：tap 为 true 用连点，否则用组合键。
func (e *Engine) SetTapMode(tap bool) {
	if tap {
		atomic.StoreInt32(&e.mode, modeTap)
	} else {
		atomic.StoreInt32(&e.mode, modeCombo)
	}
}

// SetTap 设置连点解锁参数：window 内连点 vk 键 count 次触发。
func (e *Engine) SetTap(vk uint32, count int, window time.Duration) {
	e.tapMu.Lock()
	e.tapKey = vk
	e.tapCount = count
	e.tapWindow = window
	e.tapTimes = nil
	e.tapMu.Unlock()
}

// registerTapKey 处理连点键：isTap 表示该键就是连点键（应静默拦截），
// reached 表示时间窗内已达连点次数。只在按下时计数。
func (e *Engine) registerTapKey(vk uint32, isDown bool) (isTap, reached bool) {
	e.tapMu.Lock()
	defer e.tapMu.Unlock()
	if e.tapKey == 0 || vk != e.tapKey {
		return false, false
	}
	if !isDown {
		return true, false // 连点键的抬起：静默拦截但不计数
	}
	now := time.Now()
	cutoff := now.Add(-e.tapWindow)
	kept := e.tapTimes[:0]
	for _, t := range e.tapTimes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	e.tapTimes = kept
	if len(kept) >= e.tapCount {
		e.tapTimes = e.tapTimes[:0]
		return true, true
	}
	return true, false
}

// Protect 进入保护状态：拦本地物理输入。
func (e *Engine) Protect() {
	e.clearUnlockInputTarget()
	atomic.StoreInt32(&e.state, stateProtecting)
}

// Unprotect 退出保护：一切放行。
func (e *Engine) Unprotect() {
	e.clearUnlockInputTarget()
	atomic.StoreInt32(&e.state, stateOff)
}

// BeginUnlock 原子进入验证态。只有保护态可以启动一次密码验证。
func (e *Engine) BeginUnlock() bool {
	if !atomic.CompareAndSwapInt32(&e.state, stateProtecting, stateUnlocking) {
		return false
	}
	e.clearUnlockInputTarget()
	return true
}

// RegisterUnlockInputTarget 将验证态的键盘输入限制到当前密码框。
// 返回值撤销本次注册；对话框关闭时必须调用。
func RegisterUnlockInputTarget(dialog, input uintptr) func() {
	activeMu.Lock()
	engine := activeEngine
	activeMu.Unlock()
	if engine == nil || dialog == 0 || input == 0 {
		return func() {}
	}
	engine.targetMu.Lock()
	engine.unlockDialog = dialog
	engine.unlockInput = input
	engine.targetMu.Unlock()
	return func() {
		engine.targetMu.Lock()
		if engine.unlockDialog == dialog && engine.unlockInput == input {
			engine.unlockDialog = 0
			engine.unlockInput = 0
		}
		engine.targetMu.Unlock()
	}
}

func (e *Engine) clearUnlockInputTarget() {
	e.targetMu.Lock()
	e.unlockDialog = 0
	e.unlockInput = 0
	e.targetMu.Unlock()
}

func (e *Engine) passwordInputActive() bool {
	e.targetMu.RLock()
	dialog, input := e.unlockDialog, e.unlockInput
	active := e.unlockInputActive
	e.targetMu.RUnlock()
	return dialog != 0 && input != 0 && active != nil && active(dialog, input)
}

type guiThreadInfo struct {
	Size, Flags                                  uint32
	Active, Focus, Capture, MenuOwner            uintptr
	MoveSize, Caret                              uintptr
	CaretLeft, CaretTop, CaretRight, CaretBottom int32
}

func nativeUnlockInputActive(dialog, input uintptr) bool {
	foreground, _, _ := procGetForeground.Call()
	if foreground != dialog {
		return false
	}
	threadID, _, _ := procGetWindowThread.Call(dialog, 0)
	if threadID == 0 {
		return false
	}
	info := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	ok, _, _ := procGetGUIThread.Call(threadID, uintptr(unsafe.Pointer(&info)))
	return ok != 0 && info.Active == dialog && info.Focus == input
}

func isPasswordInputKey(vk uint32) bool {
	if (vk >= '0' && vk <= '9') || (vk >= 'A' && vk <= 'Z') ||
		(vk >= 0x60 && vk <= 0x6F) || (vk >= 0xBA && vk <= 0xE2) {
		return true
	}
	switch vk {
	case 0x08, 0x0D, 0x1B, 0x20, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2E,
		vkShift, vkLShift, vkRShift, 0x14:
		return true
	}
	return false
}

// State 返回当前状态（供测试/展示）。
func (e *Engine) State() int32 { return atomic.LoadInt32(&e.state) }

// IsProtecting 是否处于保护态。
func (e *Engine) IsProtecting() bool { return atomic.LoadInt32(&e.state) == stateProtecting }

// Start 在独立线程安装钩子并运行消息循环。阻塞直到钩子安装成功或失败。
func (e *Engine) Start() error {
	if !atomic.CompareAndSwapInt32(&e.running, 0, 1) {
		return errors.New("引擎已在运行")
	}
	activeMu.Lock()
	if activeEngine != nil {
		activeMu.Unlock()
		atomic.StoreInt32(&e.running, 0)
		return errors.New("已有钩子引擎在运行")
	}
	activeEngine = e
	activeMu.Unlock()

	go e.loop()
	return <-e.started
}

// Stop 卸载钩子并结束消息循环。
func (e *Engine) Stop() {
	if atomic.LoadInt32(&e.running) == 0 {
		return
	}
	tid := atomic.LoadUint32(&e.threadID)
	if tid != 0 {
		procPostThreadMsgW.Call(uintptr(tid), wmQuit, 0, 0)
	}
	<-e.stopped
	activeMu.Lock()
	if activeEngine == e {
		activeEngine = nil
	}
	activeMu.Unlock()
	atomic.StoreInt32(&e.running, 0)
}

// loop 运行在锁定的 OS 线程上：安装钩子 -> 跑消息循环 -> 卸载。
func (e *Engine) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid, _, _ := procGetCurrentTID.Call()
	atomic.StoreUint32(&e.threadID, uint32(tid))

	hMod, _, _ := procGetModuleHandle.Call(0)

	kb, _, err := procSetWindowsHook.Call(uintptr(whKeyboardLL), keyboardCallback, hMod, 0)
	if kb == 0 {
		e.started <- errors.New("安装键盘钩子失败: " + err.Error())
		close(e.stopped)
		return
	}
	e.kbHook = kb

	ms, _, err := procSetWindowsHook.Call(uintptr(whMouseLL), mouseCallback, hMod, 0)
	if ms == 0 {
		procUnhookWindows.Call(kb)
		e.started <- errors.New("安装鼠标钩子失败: " + err.Error())
		close(e.stopped)
		return
	}
	e.msHook = ms

	e.started <- nil

	// 消息循环：GetMessage 让系统在本线程投递钩子回调；收到 WM_QUIT 返回 0 退出。
	var msg struct {
		Hwnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      point
	}
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 { // 0=WM_QUIT, -1=错误
			break
		}
	}

	procUnhookWindows.Call(e.kbHook)
	procUnhookWindows.Call(e.msHook)
	close(e.stopped)
}

// updateModifier 根据物理键更新修饰键状态。
func (e *Engine) updateModifier(vk uint32, down bool) {
	e.modMu.Lock()
	defer e.modMu.Unlock()
	switch vk {
	case vkControl, vkLCtrl, vkRCtrl:
		e.ctrlDown = down
	case vkMenu, vkLMenu, vkRMenu:
		e.altDown = down
	case vkShift, vkLShift, vkRShift:
		e.shiftDown = down
	}
}

// matchesUnlock 判断当前物理修饰键 + 主键是否命中解锁组合键。
func (e *Engine) matchesUnlock(vk uint32) bool {
	e.hkMu.RLock()
	hk := e.unlock
	e.hkMu.RUnlock()
	if vk != hk.VK {
		return false
	}
	e.modMu.Lock()
	defer e.modMu.Unlock()
	if hk.Ctrl != e.ctrlDown {
		return false
	}
	if hk.Alt != e.altDown {
		return false
	}
	if hk.Shift != e.shiftDown {
		return false
	}
	return true
}

// isModifier 判断 vk 是否为修饰键。
func isModifier(vk uint32) bool {
	switch vk {
	case vkControl, vkLCtrl, vkRCtrl, vkMenu, vkLMenu, vkRMenu, vkShift, vkLShift, vkRShift:
		return true
	}
	return false
}

// keyboardCallback 是键盘低级钩子回调（全局函数，syscall 包装）。
// LPARAM carries a native pointer; preserve its type across the callback ABI.
var keyboardCallback = windows.NewCallback(func(nCode int32, wparam uintptr, lparam unsafe.Pointer) uintptr {
	e := activeEngine
	if e == nil || nCode != hcAction {
		return callNext(nCode, wparam, lparam)
	}
	ks := (*kbdllhookstruct)(lparam)
	injected := ks.Flags&llkhfInjected != 0
	isDown := wparam == wmKeyDown || wparam == wmSysKeyDown

	// 仅用物理事件维护修饰键状态。
	if !injected {
		e.updateModifier(ks.VkCode, isDown)
	}

	state := atomic.LoadInt32(&e.state)
	if state == stateUnlocking {
		if atomic.LoadInt32(&e.restrictUnlockInput) == 0 {
			return callNext(nCode, wparam, lparam)
		}
		if e.passwordInputActive() && isPasswordInputKey(ks.VkCode) {
			return callNext(nCode, wparam, lparam)
		}
		return 1
	}

	if state == stateProtecting && !injected {
		if atomic.LoadInt32(&e.mode) == modeTap {
			// 连点模式：连点键静默拦截并计数，达标则通知上层。
			if isTap, reached := e.registerTapKey(ks.VkCode, isDown); isTap {
				if reached && e.BeginUnlock() {
					e.signalUnlock()
				}
				return 1
			}
		} else {
			// 组合键模式：命中解锁组合键则通知上层，阻断该键。
			if isDown && !isModifier(ks.VkCode) && e.matchesUnlock(ks.VkCode) {
				if e.BeginUnlock() {
					e.signalUnlock()
				}
				return 1
			}
		}
		// 其余物理输入：上报（带具体按键）并阻断。
		if isDown {
			select {
			case e.events <- Event{Type: EventKeyboard, VK: ks.VkCode}:
			default:
			}
		}
		return 1
	}
	return callNext(nCode, wparam, lparam)
})

// signalUnlock 非阻塞地通知上层弹解锁密码框。
func (e *Engine) signalUnlock() {
	select {
	case e.unlockReq <- struct{}{}:
	default:
	}
}

// mouseCallback 是鼠标低级钩子回调。
var mouseCallback = windows.NewCallback(func(nCode int32, wparam uintptr, lparam unsafe.Pointer) uintptr {
	e := activeEngine
	if e == nil || nCode != hcAction {
		return callNext(nCode, wparam, lparam)
	}
	ms := (*msllhookstruct)(lparam)
	injected := ms.Flags&llmhfInjected != 0

	state := atomic.LoadInt32(&e.state)
	if state == stateUnlocking {
		if atomic.LoadInt32(&e.restrictUnlockInput) == 0 {
			return callNext(nCode, wparam, lparam)
		}
		return 1
	}
	if state == stateProtecting && !injected {
		// 纯移动不上报（避免刷屏），但仍阻断以冻结指针；点击/滚轮上报（带按键与坐标）。
		if btn := mouseButton(uint32(wparam)); btn != MouseNone {
			select {
			case e.events <- Event{Type: EventMouse, Button: btn, X: ms.Pt.X, Y: ms.Pt.Y}:
			default:
			}
		}
		return 1
	}
	return callNext(nCode, wparam, lparam)
})

// callNext 调用链上下一个钩子。
func callNext(nCode int32, wparam uintptr, lparam unsafe.Pointer) uintptr {
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wparam, uintptr(lparam))
	return r
}

// mouseButton 把鼠标消息映射为动作类型；非按键消息（如移动）返回 MouseNone。
func mouseButton(wparam uint32) MouseButton {
	switch wparam {
	case wmLButtonDown:
		return MouseLeft
	case wmRButtonDown:
		return MouseRight
	case wmMButtonDown:
		return MouseMiddle
	case wmMouseWheel, wmMouseHWheel:
		return MouseWheel
	case wmXButtonDown:
		return MouseX
	}
	return MouseNone
}

// ButtonName 返回鼠标动作的中文名。
func ButtonName(b MouseButton) string {
	switch b {
	case MouseLeft:
		return "左键"
	case MouseRight:
		return "右键"
	case MouseMiddle:
		return "中键"
	case MouseWheel:
		return "滚轮"
	case MouseX:
		return "侧键"
	}
	return "未知"
}

// KeyName 返回虚拟键码的可读名称，覆盖常用按键；未知键回退为十六进制。
func KeyName(vk uint32) string {
	if n, ok := vkNames[vk]; ok {
		return n
	}
	switch {
	case vk >= 'A' && vk <= 'Z':
		return string(rune(vk))
	case vk >= '0' && vk <= '9':
		return string(rune(vk))
	case vk >= 0x60 && vk <= 0x69: // 小键盘 0-9
		return fmt.Sprintf("小键盘%d", vk-0x60)
	case vk >= 0x70 && vk <= 0x87: // F1-F24
		return fmt.Sprintf("F%d", vk-0x70+1)
	}
	return fmt.Sprintf("键码0x%02X", vk)
}

// vkNames 是常用非字母数字键的名称表。
var vkNames = map[uint32]string{
	0x08: "退格", 0x09: "Tab", 0x0D: "回车", 0x1B: "ESC", 0x20: "空格",
	0x10: "Shift", 0x11: "Ctrl", 0x12: "Alt", 0x14: "大写锁定",
	0xA0: "左Shift", 0xA1: "右Shift", 0xA2: "左Ctrl", 0xA3: "右Ctrl",
	0xA4: "左Alt", 0xA5: "右Alt", 0x5B: "左Win", 0x5C: "右Win",
	0x25: "←", 0x26: "↑", 0x27: "→", 0x28: "↓",
	0x2D: "Insert", 0x2E: "Delete", 0x24: "Home", 0x23: "End",
	0x21: "PageUp", 0x22: "PageDown", 0x2C: "PrintScreen",
	0xBA: "分号;", 0xBB: "等号=", 0xBC: "逗号,", 0xBD: "减号-",
	0xBE: "句点.", 0xBF: "斜杠/", 0xC0: "反引号`",
	0xDB: "左括号[", 0xDC: "反斜杠\\", 0xDD: "右括号]", 0xDE: "引号'",
}

// DescribeEvent 返回用于日志的事件细节描述。
func DescribeEvent(ev Event) string {
	if ev.Type == EventMouse {
		return fmt.Sprintf("%s @ (%d, %d)", ButtonName(ev.Button), ev.X, ev.Y)
	}
	return KeyName(ev.VK)
}
