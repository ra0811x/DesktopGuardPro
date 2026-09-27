package agent

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const interactiveAgentMutexName = `Local\DesktopGuardProInteractiveAgent`

var createMutexW = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateMutexW")

// AcquireInteractiveAgentInstance limits the Agent to one process in each
// interactive Windows session. The Local namespace is session-scoped.
func AcquireInteractiveAgentInstance() (release func(), acquired bool, err error) {
	name, err := windows.UTF16PtrFromString(interactiveAgentMutexName)
	if err != nil {
		return func() {}, false, fmt.Errorf("encode interactive agent mutex name: %w", err)
	}
	handle, _, callErr := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return func() {}, false, fmt.Errorf("create interactive agent mutex: %w", callErr)
	}
	if errors.Is(callErr, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(windows.Handle(handle))
		return func() {}, false, nil
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = windows.CloseHandle(windows.Handle(handle)) })
	}, true, nil
}
