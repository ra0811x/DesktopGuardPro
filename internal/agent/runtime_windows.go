package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/ipc"
	coreservice "desktopguardpro/internal/service"
	platformwindows "desktopguardpro/internal/windows"

	"golang.org/x/sys/windows"
)

const agentRequestTimeout = 3 * time.Second

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	getWindowTextLength        = user32.NewProc("GetWindowTextLengthW")
	getWindowText              = user32.NewProc("GetWindowTextW")
	getLastInputInfo           = user32.NewProc("GetLastInputInfo")
	getAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
	setWindowsHookEx           = user32.NewProc("SetWindowsHookExW")
	callNextHookEx             = user32.NewProc("CallNextHookEx")
	unhookWindowsHookEx        = user32.NewProc("UnhookWindowsHookEx")
	getMessage                 = user32.NewProc("GetMessageW")
	peekMessage                = user32.NewProc("PeekMessageW")
	lastInputInfoSize   uint32 = uint32(unsafe.Sizeof(lastInputInfo{}))
)

type lastInputInfo struct {
	size uint32
	tick uint32
}

type windowsActivitySource struct {
	keyDown         [256]bool
	shortcutWasDown map[string]bool
	wheelEvents     lowLevelMouseMonitor
}

func newWindowsActivitySource() ActivitySource { return &windowsActivitySource{} }

func (source *windowsActivitySource) Sample(ctx context.Context, policy ActivityCapturePolicy) (ActivitySample, error) {
	if err := ctx.Err(); err != nil {
		return ActivitySample{}, err
	}
	sample := ActivitySample{}
	if policy.RecordKeyboardActivity || policy.RecordMouseClicks || policy.RecordHighRiskShortcuts {
		keyboard, mouse := sampleInputTransitions(&source.keyDown, func(virtualKey int) (bool, bool) {
			state, _, _ := getAsyncKeyState.Call(uintptr(virtualKey))
			return state&0x8000 != 0, state&0x0001 != 0
		})
		if policy.RecordKeyboardActivity {
			sample.KeyboardActivityCount = keyboard
		}
		if policy.RecordMouseClicks {
			sample.MouseClickCount = mouse
		}
	}
	if policy.RecordHighRiskShortcuts {
		if source.shortcutWasDown == nil {
			source.shortcutWasDown = make(map[string]bool)
		}
		sample.HighRiskShortcutCount = sampleHighRiskShortcutTransitions(source.shortcutWasDown, source.keyDown)
	}
	if policy.RecordMouseWheel {
		sample.MouseWheelCount = source.wheelEvents.Drain()
	} else if source.wheelEvents.started.Load() {
		source.wheelEvents.count.Store(0)
	}
	if !policy.RecordForegroundApplication && !policy.RecordWindowTitle {
		return sample, nil
	}
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return sample, nil
	}
	if policy.RecordWindowTitle {
		sample.WindowTitle = foregroundWindowTitle(hwnd)
	}
	if !policy.RecordForegroundApplication {
		return sample, nil
	}
	var processID uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &processID); err != nil {
		return sample, nil
	}
	sample.ProcessID = processID
	sample.ProcessImage = foregroundProcessImage(processID)
	return sample, nil
}

func sampleHighRiskShortcutTransitions(previous map[string]bool, keys [256]bool) map[string]uint64 {
	if previous == nil {
		return nil
	}
	ctrl := keys[0x11] || keys[0xA2] || keys[0xA3]
	shift := keys[0x10] || keys[0xA0] || keys[0xA1]
	alt := keys[0x12] || keys[0xA4] || keys[0xA5]
	win := keys[0x5B] || keys[0x5C]
	current := map[string]bool{
		"alt_tab":      alt && keys[0x09],
		"win_run":      win && keys[0x52],
		"win_lock":     win && keys[0x4C],
		"task_manager": ctrl && shift && keys[0x1B],
	}
	counts := make(map[string]uint64)
	for category, down := range current {
		if down && !previous[category] {
			counts[category] = 1
		}
		previous[category] = down
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

const (
	windowsHookLowLevelMouse  = 14
	windowsMessageMouseWheel  = 0x020A
	windowsMessageMouseHWheel = 0x020E
)

type lowLevelMouseMonitor struct {
	once     sync.Once
	started  atomic.Bool
	count    atomic.Uint64
	callback uintptr
}

type windowsMessage struct {
	WindowHandle uintptr
	Message      uint32
	WParam       uintptr
	LParam       uintptr
	Time         uint32
	PointX       int32
	PointY       int32
	Private      uint32
}

func (monitor *lowLevelMouseMonitor) Drain() uint64 {
	monitor.once.Do(func() {
		monitor.started.Store(true)
		go monitor.run()
	})
	return monitor.count.Swap(0)
}

func (monitor *lowLevelMouseMonitor) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	monitor.callback = syscall.NewCallback(func(code int, message uintptr, event uintptr) uintptr {
		if code >= 0 && isMouseWheelMessage(message) {
			monitor.count.Add(1)
		}
		result, _, _ := callNextHookEx.Call(0, uintptr(code), message, event)
		return result
	})
	hook, _, _ := setWindowsHookEx.Call(windowsHookLowLevelMouse, monitor.callback, 0, 0)
	if hook == 0 {
		return
	}
	defer unhookWindowsHookEx.Call(hook)
	var message windowsMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
	}
}

func isMouseWheelMessage(message uintptr) bool {
	return message == windowsMessageMouseWheel || message == windowsMessageMouseHWheel
}

func sampleInputTransitions(previous *[256]bool, state func(int) (bool, bool)) (uint64, uint64) {
	if previous == nil || state == nil {
		return 0, 0
	}
	var keyboard, mouse uint64
	for virtualKey := 1; virtualKey < len(previous); virtualKey++ {
		down, pressedSinceLastSample := state(virtualKey)
		pressed := pressedSinceLastSample || (down && !previous[virtualKey])
		previous[virtualKey] = down
		if !pressed {
			continue
		}
		switch virtualKey {
		case 0x01, 0x02, 0x04, 0x05, 0x06:
			mouse++
		default:
			keyboard++
		}
	}
	return keyboard, mouse
}

func foregroundWindowTitle(hwnd windows.HWND) string {
	length, _, _ := getWindowTextLength.Call(uintptr(hwnd))
	if length == 0 || length > 32_768 {
		return ""
	}
	buffer := make([]uint16, length+1)
	read, _, _ := getWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if read == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:read])
}

func foregroundProcessImage(processID uint32) string {
	if processID == 0 {
		return ""
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buffer[:size])
}

type pipeActivityReporter struct{}

func newPipeActivityReporter() pipeActivityReporter { return pipeActivityReporter{} }

func (pipeActivityReporter) CurrentActivityPolicy(ctx context.Context) (ActivityPolicy, error) {
	requestContext, cancel := context.WithTimeout(ctx, agentRequestTimeout)
	defer cancel()
	current, err := callService(requestContext, contracts.MessageTypeSessionCurrentGet, struct{}{}, contracts.MessageTypeSessionResult)
	if err != nil {
		return ActivityPolicy{}, err
	}
	var session coreservice.SessionResult
	if err := current.DecodePayload(&session); err != nil {
		return ActivityPolicy{}, errors.New("decode current protection session")
	}
	if session.Session == nil || (session.Session.State != "active" && session.Session.State != "degraded") {
		return ActivityPolicy{Enabled: false, SampleInterval: defaultSampleInterval, ReportInterval: defaultReportInterval}, nil
	}
	policy := session.Session.MonitoringPolicy.Resolved()
	activity := ActivityPolicy{
		Enabled:        policy.UserSessionActivityEnabled,
		SampleInterval: time.Duration(policy.UserSession.SampleIntervalSeconds) * time.Second,
		ReportInterval: time.Duration(policy.UserSession.ReportIntervalSeconds) * time.Second,
		Capture:        activityCapturePolicyForMonitoringPolicy(policy),
	}
	controlResponse, err := callService(requestContext, contracts.MessageTypeInputControlGet, struct{}{}, contracts.MessageTypeInputControlResult)
	if err != nil {
		return ActivityPolicy{}, err
	}
	var control coreservice.InputControlResult
	if err := controlResponse.DecodePayload(&control); err != nil {
		return ActivityPolicy{}, errors.New("decode temporary input control")
	}
	activity.Capture = activityCapturePolicyForInputControl(activity.Capture, control.Enabled && control.Source == "temporary")
	return activity, nil
}

func activityCapturePolicyForMonitoringPolicy(policy domain.MonitoringPolicy) ActivityCapturePolicy {
	policy = policy.Resolved()
	capture := ActivityCapturePolicy{
		RecordForegroundApplication: policy.UserSession.RecordForegroundApplication,
		RecordWindowTitle:           policy.UserSession.RecordWindowTitle,
		RecordKeyboardActivity:      policy.UserSession.RecordKeyboardActivity,
		RecordMouseClicks:           policy.UserSession.RecordMouseClicks,
		RecordMouseWheel:            policy.UserSession.RecordMouseWheel,
		RecordHighRiskShortcuts:     policy.UserSession.RecordHighRiskShortcuts,
	}
	return activityCapturePolicyForInputControl(capture, policy.InputShieldEnabled)
}

func activityCapturePolicyForInputControl(capture ActivityCapturePolicy, active bool) ActivityCapturePolicy {
	if !active {
		return capture
	}
	capture.RecordKeyboardActivity = false
	capture.RecordMouseClicks = false
	capture.RecordMouseWheel = false
	capture.RecordHighRiskShortcuts = false
	return capture
}

func (pipeActivityReporter) Report(ctx context.Context, summary ActivitySummary) error {
	requestContext, cancel := context.WithTimeout(ctx, agentRequestTimeout)
	defer cancel()
	current, err := callService(requestContext, contracts.MessageTypeSessionCurrentGet, struct{}{}, contracts.MessageTypeSessionResult)
	if err != nil {
		return err
	}
	var session coreservice.SessionResult
	if err := current.DecodePayload(&session); err != nil || session.Session == nil {
		return errors.New("decode current protection session")
	}
	if session.Session.State != "active" && session.Session.State != "degraded" {
		return nil
	}
	_, err = callService(requestContext, contracts.MessageTypeAgentActivityReport, coreservice.AgentActivityReportRequest{
		SessionID: session.Session.ID, WindowTitle: summary.WindowTitle, ProcessID: summary.ProcessID,
		ProcessImage: summary.ProcessImage, InputActivityCount: summary.InputActivityCount,
		KeyboardActivityCount: summary.KeyboardActivityCount, MouseClickCount: summary.MouseClickCount,
		MouseWheelCount: summary.MouseWheelCount, ActivityStartUTC: summary.ActivityStartUTC,
		HighRiskShortcutCount: summary.HighRiskShortcutCount,
		ActivityEndUTC:        summary.ActivityEndUTC, ForegroundStartUTC: summary.ForegroundStartUTC,
		ForegroundEndUTC: summary.ForegroundEndUTC, ForegroundDurationMS: summary.ForegroundDurationMS,
		ObservedUTC: summary.ObservedUTC,
	}, contracts.MessageTypeAgentActivityResult)
	return err
}

func callService(
	ctx context.Context,
	messageType contracts.MessageType,
	payload any,
	wantType contracts.MessageType,
) (contracts.Message, error) {
	requestID, err := agentRequestID()
	if err != nil {
		return contracts.Message{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return contracts.Message{}, errors.New("agent request context has no deadline")
	}
	request, err := contracts.NewMessage(requestID, messageType, deadline.UTC(), payload)
	if err != nil {
		return contracts.Message{}, fmt.Errorf("create service request: %w", err)
	}
	connection, err := platformwindows.DialPipe(ctx, platformwindows.ControlPipePath)
	if err != nil {
		return contracts.Message{}, fmt.Errorf("connect to service: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(deadline); err != nil {
		return contracts.Message{}, fmt.Errorf("set service deadline: %w", err)
	}
	if err := ipc.WriteMessage(connection, request); err != nil {
		return contracts.Message{}, fmt.Errorf("write service request: %w", err)
	}
	response, err := ipc.ReadMessage(connection)
	if err != nil {
		return contracts.Message{}, fmt.Errorf("read service response: %w", err)
	}
	if response.RequestID != requestID {
		return contracts.Message{}, errors.New("service response request id mismatch")
	}
	if response.Type == contracts.MessageTypeError {
		var failure coreservice.ErrorResult
		if err := response.DecodePayload(&failure); err != nil {
			return contracts.Message{}, fmt.Errorf("decode service error: %w", err)
		}
		return contracts.Message{}, fmt.Errorf("service error %s: %s", failure.Code, failure.Message)
	}
	if response.Type != wantType {
		return contracts.Message{}, fmt.Errorf("unexpected service response type %q", response.Type)
	}
	return response, nil
}

func agentRequestID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create agent request id: %w", err)
	}
	return hex.EncodeToString(data), nil
}
