package agent

import (
	"errors"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	inputDeviceTypeMouse           = 0
	inputDeviceTypeKeyboard        = 1
	inputDeviceTypeUnknown         = ^uint32(0)
	inputDeviceInfoName            = 0x20000007
	inputDeviceReadHeader          = 0x10000005
	inputDeviceReceiveInBackground = 0x00000100
	inputDeviceNotifyChanges       = 0x00002000
	inputDeviceMessageInputChange  = 0x00FE
	inputDeviceMessageInput        = 0x00FF
	inputDeviceChangeArrival       = 1
	inputDeviceChangeRemoval       = 2
	inputDeviceMessageOnlyWindow   = ^uintptr(2)
)

var (
	inputDeviceGetList       = user32.NewProc("GetRawInputDeviceList")
	inputDeviceGetInfo       = user32.NewProc("GetRawInputDeviceInfoW")
	inputDeviceRegister      = user32.NewProc("RegisterRawInputDevices")
	inputDeviceGetData       = user32.NewProc("GetRawInputData")
	inputDeviceWindowProc    = syscall.NewCallback(inputDeviceTrackerWindowProc)
	activeInputDeviceTracker atomic.Pointer[inputDeviceTracker]
)

type inputRawDeviceList struct {
	Handle uintptr
	Type   uint32
}

type inputRawDeviceRegistration struct {
	UsagePage uint16
	Usage     uint16
	Flags     uint32
	Target    uintptr
}

type inputRawHeader struct {
	Type   uint32
	Size   uint32
	Device uintptr
	WParam uintptr
}

type InputDeviceIdentity struct {
	Kind          string    `json:"kind"`
	InterfacePath string    `json:"interfacePath"`
	InstanceID    string    `json:"instanceId"`
	VendorID      string    `json:"vendorId,omitempty"`
	ProductID     string    `json:"productId,omitempty"`
	Active        bool      `json:"active"`
	LastActiveUTC time.Time `json:"lastActiveUtc,omitempty"`
	Handle        uintptr   `json:"-"`
}

type InputDeviceChange struct {
	Action string              `json:"action"`
	Device InputDeviceIdentity `json:"device"`
}

type inputDeviceTracker struct {
	threadID atomic.Uint32
	dropped  atomic.Uint64
	events   chan InputDeviceChange
	stopped  chan struct{}

	mu          sync.RWMutex
	known       map[uintptr]InputDeviceIdentity
	lastActive  map[uintptr]time.Time
	activeKB    uintptr
	activeMouse uintptr
}

func newInputDeviceTracker() *inputDeviceTracker {
	return &inputDeviceTracker{
		events: make(chan InputDeviceChange, 32), known: make(map[uintptr]InputDeviceIdentity),
		lastActive: make(map[uintptr]time.Time),
	}
}

func (tracker *inputDeviceTracker) Start() error {
	if !activeInputDeviceTracker.CompareAndSwap(nil, tracker) {
		return errors.New("another input device tracker is already running")
	}
	tracker.stopped = make(chan struct{})
	started := make(chan error, 1)
	go tracker.run(started)
	if err := <-started; err != nil {
		activeInputDeviceTracker.CompareAndSwap(tracker, nil)
		return err
	}
	return nil
}

func (tracker *inputDeviceTracker) Stop() {
	threadID := tracker.threadID.Load()
	if threadID == 0 || tracker.stopped == nil {
		return
	}
	inputShieldPostThreadMessage.Call(uintptr(threadID), windowsMessageQuit, 0, 0)
	<-tracker.stopped
	activeInputDeviceTracker.CompareAndSwap(tracker, nil)
}

func (tracker *inputDeviceTracker) Events() <-chan InputDeviceChange { return tracker.events }
func (tracker *inputDeviceTracker) DroppedEvents() uint64            { return tracker.dropped.Load() }

func (tracker *inputDeviceTracker) Snapshot() []InputDeviceIdentity {
	devices := enumerateInputDevices()
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	for index := range devices {
		devices[index].Active = (devices[index].Kind == "keyboard" && devices[index].Handle == tracker.activeKB) ||
			(devices[index].Kind == "mouse" && devices[index].Handle == tracker.activeMouse)
		devices[index].LastActiveUTC = tracker.lastActive[devices[index].Handle]
	}
	sort.Slice(devices, func(left, right int) bool {
		if devices[left].Kind != devices[right].Kind {
			return devices[left].Kind < devices[right].Kind
		}
		return devices[left].InstanceID < devices[right].InstanceID
	})
	return devices
}

func (tracker *inputDeviceTracker) run(started chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(tracker.stopped)
	tracker.threadID.Store(windows.GetCurrentThreadId())

	module, _, _ := inputShieldGetModuleHandle.Call(0)
	className, _ := windows.UTF16PtrFromString("DesktopGuardProInputDevices")
	class := inputOverlayWindowClass{
		Size: uint32(unsafe.Sizeof(inputOverlayWindowClass{})), WindowProc: inputDeviceWindowProc,
		Instance: module, ClassName: className,
	}
	if registered, _, registerErr := inputOverlayRegisterClass.Call(uintptr(unsafe.Pointer(&class))); registered == 0 &&
		!errors.Is(registerErr, windows.ERROR_CLASS_ALREADY_EXISTS) {
		started <- errors.New("register input device tracker: " + registerErr.Error())
		return
	}
	windowTitle, _ := windows.UTF16PtrFromString("DesktopGuardPro input devices")
	window, _, createErr := inputOverlayCreateWindow.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowTitle)), 0,
		0, 0, 0, 0, inputDeviceMessageOnlyWindow, 0, module, 0,
	)
	if window == 0 {
		started <- errors.New("create input device tracker window: " + createErr.Error())
		return
	}
	defer inputOverlayDestroyWindow.Call(window)
	registrations := [2]inputRawDeviceRegistration{
		{UsagePage: 0x01, Usage: 0x02, Flags: inputDeviceReceiveInBackground | inputDeviceNotifyChanges, Target: window},
		{UsagePage: 0x01, Usage: 0x06, Flags: inputDeviceReceiveInBackground | inputDeviceNotifyChanges, Target: window},
	}
	if registered, _, registerErr := inputDeviceRegister.Call(
		uintptr(unsafe.Pointer(&registrations[0])), uintptr(len(registrations)), unsafe.Sizeof(registrations[0]),
	); registered == 0 {
		started <- errors.New("register raw input devices: " + registerErr.Error())
		return
	}
	tracker.replaceKnown(enumerateInputDevices())
	started <- nil

	var message windowsMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		inputOverlayDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func inputDeviceTrackerWindowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	tracker := activeInputDeviceTracker.Load()
	if tracker == nil {
		result, _, _ := inputOverlayDefWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	}
	switch message {
	case inputDeviceMessageInput:
		tracker.recordRawInput(lParam)
		return 0
	case inputDeviceMessageInputChange:
		tracker.recordDeviceChange(wParam, lParam)
		return 0
	default:
		result, _, _ := inputOverlayDefWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	}
}

func (tracker *inputDeviceTracker) recordRawInput(rawInput uintptr) {
	header := inputRawHeader{}
	size := uint32(unsafe.Sizeof(header))
	read, _, _ := inputDeviceGetData.Call(rawInput, inputDeviceReadHeader, uintptr(unsafe.Pointer(&header)),
		uintptr(unsafe.Pointer(&size)), unsafe.Sizeof(header))
	if int32(read) < 0 || header.Device == 0 {
		return
	}
	tracker.mu.Lock()
	switch header.Type {
	case inputDeviceTypeKeyboard:
		tracker.activeKB = header.Device
	case inputDeviceTypeMouse:
		tracker.activeMouse = header.Device
	default:
		tracker.mu.Unlock()
		return
	}
	tracker.lastActive[header.Device] = time.Now().UTC()
	tracker.mu.Unlock()
}

func (tracker *inputDeviceTracker) recordDeviceChange(change uintptr, handle uintptr) {
	if handle == 0 {
		return
	}
	tracker.mu.Lock()
	device := tracker.known[handle]
	action := ""
	switch change {
	case inputDeviceChangeArrival:
		device = inputDeviceFromHandle(handle, inputDeviceTypeUnknown)
		tracker.known[handle] = device
		action = "connected"
	case inputDeviceChangeRemoval:
		delete(tracker.known, handle)
		delete(tracker.lastActive, handle)
		if tracker.activeKB == handle {
			tracker.activeKB = 0
		}
		if tracker.activeMouse == handle {
			tracker.activeMouse = 0
		}
		action = "removed"
	}
	tracker.mu.Unlock()
	if action == "" {
		return
	}
	select {
	case tracker.events <- InputDeviceChange{Action: action, Device: device}:
	default:
		tracker.dropped.Add(1)
	}
}

func (tracker *inputDeviceTracker) replaceKnown(devices []InputDeviceIdentity) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.known = make(map[uintptr]InputDeviceIdentity, len(devices))
	for _, device := range devices {
		tracker.known[device.Handle] = device
	}
}

func enumerateInputDevices() []InputDeviceIdentity {
	var count uint32
	inputDeviceGetList.Call(0, uintptr(unsafe.Pointer(&count)), unsafe.Sizeof(inputRawDeviceList{}))
	if count == 0 {
		return nil
	}
	list := make([]inputRawDeviceList, count)
	read, _, _ := inputDeviceGetList.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&count)),
		unsafe.Sizeof(inputRawDeviceList{}))
	if int32(read) < 0 {
		return nil
	}
	devices := make([]InputDeviceIdentity, 0, count)
	for index := uint32(0); index < count; index++ {
		if device := inputDeviceFromHandle(list[index].Handle, list[index].Type); device.Kind != "" && device.InterfacePath != "" {
			devices = append(devices, device)
		}
	}
	return devices
}

func inputDeviceFromHandle(handle uintptr, fallbackType uint32) InputDeviceIdentity {
	deviceType := fallbackType
	if fallbackType == inputDeviceTypeUnknown {
		deviceType = inputDeviceTypeFromList(handle)
	}
	kind := inputDeviceKind(deviceType)
	path := inputDevicePath(handle)
	vendorID, productID := inputDeviceUSBIDs(path)
	return InputDeviceIdentity{
		Kind: kind, InterfacePath: path, InstanceID: inputDeviceInstanceID(path),
		VendorID: vendorID, ProductID: productID, Handle: handle,
	}
}

func inputDeviceTypeFromList(handle uintptr) uint32 {
	var count uint32
	inputDeviceGetList.Call(0, uintptr(unsafe.Pointer(&count)), unsafe.Sizeof(inputRawDeviceList{}))
	if count == 0 {
		return inputDeviceTypeUnknown
	}
	list := make([]inputRawDeviceList, count)
	read, _, _ := inputDeviceGetList.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&count)),
		unsafe.Sizeof(inputRawDeviceList{}))
	if int32(read) < 0 {
		return inputDeviceTypeUnknown
	}
	for index := uint32(0); index < count; index++ {
		if list[index].Handle == handle {
			return list[index].Type
		}
	}
	return inputDeviceTypeUnknown
}

func inputDeviceKind(deviceType uint32) string {
	switch deviceType {
	case inputDeviceTypeKeyboard:
		return "keyboard"
	case inputDeviceTypeMouse:
		return "mouse"
	default:
		return ""
	}
}

func inputDevicePath(handle uintptr) string {
	var size uint32
	inputDeviceGetInfo.Call(handle, inputDeviceInfoName, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 || size > 32_768 {
		return ""
	}
	buffer := make([]uint16, size+1)
	result, _, _ := inputDeviceGetInfo.Call(handle, inputDeviceInfoName, uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)))
	if int32(result) < 0 {
		return ""
	}
	return windows.UTF16ToString(buffer)
}

func inputDeviceInstanceID(path string) string {
	result := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), `\\?\`), `\??\`)
	if index := strings.LastIndex(result, "#{"); index >= 0 {
		result = result[:index]
	}
	return strings.ToUpper(strings.ReplaceAll(result, "#", `\`))
}

func inputDeviceUSBIDs(path string) (string, string) {
	upper := strings.ToUpper(path)
	return inputDeviceHexID(upper, "VID_"), inputDeviceHexID(upper, "PID_")
}

func inputDeviceHexID(value, prefix string) string {
	index := strings.Index(value, prefix)
	if index < 0 || index+len(prefix)+4 > len(value) {
		return ""
	}
	id := value[index+len(prefix) : index+len(prefix)+4]
	if _, err := strconv.ParseUint(id, 16, 16); err != nil {
		return ""
	}
	return id
}
