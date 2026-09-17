package agent

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	inputOverlayWindowPopup       = 0x80000000
	inputOverlayExTopmost         = 0x00000008
	inputOverlayExTransparent     = 0x00000020
	inputOverlayExToolWindow      = 0x00000080
	inputOverlayExLayered         = 0x00080000
	inputOverlayExNoActivate      = 0x08000000
	inputOverlayMessageDestroy    = 0x0002
	inputOverlayMessagePaint      = 0x000F
	inputOverlayMessageTimer      = 0x0113
	inputOverlayMessageShow       = 0x8001
	inputOverlayShowNoActivate    = 4
	inputOverlayHide              = 0
	inputOverlayLayeredAlpha      = 0x02
	inputOverlayTimerID           = 1
	inputOverlayTextCenter        = 0x0001
	inputOverlayTextVCenter       = 0x0004
	inputOverlayTextSingleLine    = 0x0020
	inputOverlayTransparentBkMode = 1
)

var (
	inputOverlayGDI32            = windows.NewLazySystemDLL("gdi32.dll")
	inputOverlayRegisterClass    = user32.NewProc("RegisterClassExW")
	inputOverlayCreateWindow     = user32.NewProc("CreateWindowExW")
	inputOverlayDefWindowProc    = user32.NewProc("DefWindowProcW")
	inputOverlayTranslateMessage = user32.NewProc("TranslateMessage")
	inputOverlayDispatchMessage  = user32.NewProc("DispatchMessageW")
	inputOverlayPostMessage      = user32.NewProc("PostMessageW")
	inputOverlayShowWindow       = user32.NewProc("ShowWindow")
	inputOverlayMoveWindow       = user32.NewProc("MoveWindow")
	inputOverlaySetLayered       = user32.NewProc("SetLayeredWindowAttributes")
	inputOverlayEnumMonitors     = user32.NewProc("EnumDisplayMonitors")
	inputOverlayBeginPaint       = user32.NewProc("BeginPaint")
	inputOverlayEndPaint         = user32.NewProc("EndPaint")
	inputOverlayFillRect         = user32.NewProc("FillRect")
	inputOverlayDrawText         = user32.NewProc("DrawTextW")
	inputOverlaySetTimer         = user32.NewProc("SetTimer")
	inputOverlayKillTimer        = user32.NewProc("KillTimer")
	inputOverlayGetClientRect    = user32.NewProc("GetClientRect")
	inputOverlayDestroyWindow    = user32.NewProc("DestroyWindow")
	inputOverlayCreateSolidBrush = inputOverlayGDI32.NewProc("CreateSolidBrush")
	inputOverlaySetBkMode        = inputOverlayGDI32.NewProc("SetBkMode")
	inputOverlaySetTextColor     = inputOverlayGDI32.NewProc("SetTextColor")
	inputOverlayCreateFont       = inputOverlayGDI32.NewProc("CreateFontW")
	inputOverlaySelectObject     = inputOverlayGDI32.NewProc("SelectObject")
	inputOverlayDeleteObject     = inputOverlayGDI32.NewProc("DeleteObject")
	activeInputWarningOverlay    atomic.Pointer[inputWarningOverlay]
	inputOverlayWindowProc       = syscall.NewCallback(inputWarningOverlayWindowProc)
	inputOverlayMonitorEnumProc  = syscall.NewCallback(inputWarningOverlayMonitorProc)
)

type inputOverlayRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type inputOverlayWindowClass struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type inputOverlayPaint struct {
	DeviceContext uintptr
	Erase         int32
	Paint         inputOverlayRect
	Restore       int32
	IncUpdate     int32
	Reserved      [32]byte
}

type inputWarningOverlay struct {
	threadID atomic.Uint32
	primary  atomic.Uintptr

	mu       sync.RWMutex
	message  string
	duration time.Duration

	windows []uintptr
	layouts []inputOverlayRect
	brush   uintptr
	font    uintptr
	stopped chan struct{}
}

func newInputWarningOverlay() *inputWarningOverlay {
	return &inputWarningOverlay{duration: 5 * time.Second}
}

func (overlay *inputWarningOverlay) Start() error {
	if !activeInputWarningOverlay.CompareAndSwap(nil, overlay) {
		return errors.New("another input warning overlay is already running")
	}
	overlay.stopped = make(chan struct{})
	started := make(chan error, 1)
	go overlay.run(started)
	if err := <-started; err != nil {
		activeInputWarningOverlay.CompareAndSwap(overlay, nil)
		return err
	}
	return nil
}

func (overlay *inputWarningOverlay) Show(message string, duration time.Duration) {
	if duration <= 0 {
		duration = 5 * time.Second
	}
	overlay.mu.Lock()
	overlay.message = message
	overlay.duration = duration
	overlay.mu.Unlock()
	window := overlay.primary.Load()
	if window != 0 {
		inputOverlayPostMessage.Call(window, inputOverlayMessageShow, 0, 0)
	}
}

func (overlay *inputWarningOverlay) Stop() {
	threadID := overlay.threadID.Load()
	if threadID == 0 || overlay.stopped == nil {
		return
	}
	inputShieldPostThreadMessage.Call(uintptr(threadID), windowsMessageQuit, 0, 0)
	<-overlay.stopped
	activeInputWarningOverlay.CompareAndSwap(overlay, nil)
}

func (overlay *inputWarningOverlay) run(started chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(overlay.stopped)
	overlay.threadID.Store(windows.GetCurrentThreadId())

	module, _, _ := inputShieldGetModuleHandle.Call(0)
	className, _ := windows.UTF16PtrFromString("DesktopGuardProInputWarning")
	overlay.brush, _, _ = inputOverlayCreateSolidBrush.Call(inputOverlayColor(116, 38, 45))
	fontName, _ := windows.UTF16PtrFromString("Microsoft YaHei UI")
	fontHeight := int32(-30)
	overlay.font, _, _ = inputOverlayCreateFont.Call(
		uintptr(fontHeight), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fontName)),
	)
	class := inputOverlayWindowClass{
		Size: uint32(unsafe.Sizeof(inputOverlayWindowClass{})), WindowProc: inputOverlayWindowProc,
		Instance: module, Background: overlay.brush, ClassName: className,
	}
	if registered, _, registerErr := inputOverlayRegisterClass.Call(uintptr(unsafe.Pointer(&class))); registered == 0 {
		started <- errors.New("register input warning overlay: " + registerErr.Error())
		overlay.releaseGraphics()
		return
	}
	monitors := enumerateInputOverlayMonitors()
	overlay.layouts = inputWarningOverlayLayouts(monitors)
	if len(overlay.layouts) == 0 {
		started <- errors.New("input warning overlay found no displays")
		overlay.releaseGraphics()
		return
	}
	windowTitle, _ := windows.UTF16PtrFromString("DesktopGuardPro input protection")
	for _, layout := range overlay.layouts {
		window, _, _ := inputOverlayCreateWindow.Call(
			inputOverlayExTopmost|inputOverlayExTransparent|inputOverlayExToolWindow|inputOverlayExLayered|inputOverlayExNoActivate,
			uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowTitle)), inputOverlayWindowPopup,
			uintptr(layout.Left), uintptr(layout.Top), uintptr(layout.Right-layout.Left), uintptr(layout.Bottom-layout.Top),
			0, 0, module, 0,
		)
		if window == 0 {
			continue
		}
		inputOverlaySetLayered.Call(window, 0, 235, inputOverlayLayeredAlpha)
		overlay.windows = append(overlay.windows, window)
	}
	if len(overlay.windows) == 0 {
		started <- errors.New("create input warning overlay windows")
		overlay.releaseGraphics()
		return
	}
	overlay.primary.Store(overlay.windows[0])
	started <- nil

	var message windowsMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		inputOverlayTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		inputOverlayDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
	overlay.primary.Store(0)
	for _, window := range overlay.windows {
		inputOverlayDestroyWindow.Call(window)
	}
	overlay.windows = nil
	overlay.releaseGraphics()
}

func (overlay *inputWarningOverlay) showAll() {
	overlay.mu.RLock()
	duration := overlay.duration
	overlay.mu.RUnlock()
	durationMS := duration.Milliseconds()
	if durationMS < 1 {
		durationMS = 1
	}
	for index, window := range overlay.windows {
		layout := overlay.layouts[index]
		inputOverlayMoveWindow.Call(window, uintptr(layout.Left), uintptr(layout.Top),
			uintptr(layout.Right-layout.Left), uintptr(layout.Bottom-layout.Top), 1)
		inputOverlayShowWindow.Call(window, inputOverlayShowNoActivate)
		inputOverlayKillTimer.Call(window, inputOverlayTimerID)
		inputOverlaySetTimer.Call(window, inputOverlayTimerID, uintptr(durationMS), 0)
	}
}

func (overlay *inputWarningOverlay) releaseGraphics() {
	if overlay.font != 0 {
		inputOverlayDeleteObject.Call(overlay.font)
		overlay.font = 0
	}
	if overlay.brush != 0 {
		inputOverlayDeleteObject.Call(overlay.brush)
		overlay.brush = 0
	}
}

func enumerateInputOverlayMonitors() []inputOverlayRect {
	overlay := activeInputWarningOverlay.Load()
	if overlay == nil {
		return nil
	}
	overlay.layouts = nil
	inputOverlayEnumMonitors.Call(0, 0, inputOverlayMonitorEnumProc, 0)
	monitors := append([]inputOverlayRect(nil), overlay.layouts...)
	overlay.layouts = nil
	return monitors
}

func inputWarningOverlayMonitorProc(_ uintptr, _ uintptr, monitorRect uintptr, _ uintptr) uintptr {
	overlay := activeInputWarningOverlay.Load()
	if overlay == nil || monitorRect == 0 {
		return 0
	}
	var rect inputOverlayRect
	inputShieldRtlMoveMemory.Call(uintptr(unsafe.Pointer(&rect)), monitorRect, unsafe.Sizeof(rect))
	overlay.layouts = append(overlay.layouts, rect)
	return 1
}

func inputWarningOverlayWindowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	overlay := activeInputWarningOverlay.Load()
	if overlay == nil {
		result, _, _ := inputOverlayDefWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	}
	switch message {
	case inputOverlayMessageShow:
		overlay.showAll()
		return 0
	case inputOverlayMessageTimer:
		inputOverlayKillTimer.Call(window, inputOverlayTimerID)
		inputOverlayShowWindow.Call(window, inputOverlayHide)
		return 0
	case inputOverlayMessagePaint:
		overlay.paint(window)
		return 0
	case inputOverlayMessageDestroy:
		return 0
	default:
		result, _, _ := inputOverlayDefWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	}
}

func (overlay *inputWarningOverlay) paint(window uintptr) {
	var paint inputOverlayPaint
	deviceContext, _, _ := inputOverlayBeginPaint.Call(window, uintptr(unsafe.Pointer(&paint)))
	defer inputOverlayEndPaint.Call(window, uintptr(unsafe.Pointer(&paint)))
	var client inputOverlayRect
	inputOverlayGetClientRect.Call(window, uintptr(unsafe.Pointer(&client)))
	inputOverlayFillRect.Call(deviceContext, uintptr(unsafe.Pointer(&client)), overlay.brush)
	inputOverlaySelectObject.Call(deviceContext, overlay.font)
	inputOverlaySetBkMode.Call(deviceContext, inputOverlayTransparentBkMode)
	inputOverlaySetTextColor.Call(deviceContext, inputOverlayColor(255, 255, 255))
	overlay.mu.RLock()
	message := overlay.message
	overlay.mu.RUnlock()
	text, _ := windows.UTF16FromString(message)
	inputOverlayDrawText.Call(deviceContext, uintptr(unsafe.Pointer(&text[0])), ^uintptr(0),
		uintptr(unsafe.Pointer(&client)), inputOverlayTextCenter|inputOverlayTextVCenter|inputOverlayTextSingleLine)
}

func inputWarningOverlayLayouts(monitors []inputOverlayRect) []inputOverlayRect {
	layouts := make([]inputOverlayRect, 0, len(monitors))
	for _, monitor := range monitors {
		width := monitor.Right - monitor.Left
		height := monitor.Bottom - monitor.Top
		if width <= 0 || height <= 0 {
			continue
		}
		bannerHeight := height / 7
		if bannerHeight < 96 {
			bannerHeight = 96
		}
		if bannerHeight > 160 {
			bannerHeight = 160
		}
		top := monitor.Top + (height-bannerHeight)/2
		layouts = append(layouts, inputOverlayRect{
			Left: monitor.Left, Top: top, Right: monitor.Right, Bottom: top + bannerHeight,
		})
	}
	return layouts
}

func inputOverlayColor(red, green, blue uint32) uintptr {
	return uintptr(red | green<<8 | blue<<16)
}
