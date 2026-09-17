package windows

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	coreservice "desktopguardpro/internal/service"

	winapi "golang.org/x/sys/windows"
)

const (
	credUIWinGeneric                = 0x00000001
	credUIWinSecurePrompt           = 0x00001000
	endSessionCredentialPromptFlags = 0
	windowsErrorInsufficientData    = 122
	windowsErrorCancelled           = 1223
)

var (
	ErrSystemCredentialPromptCancelled = errors.New("system credential prompt was cancelled")
	procCredUIPrompt                   = winapi.NewLazySystemDLL("credui.dll").NewProc("CredUIPromptForWindowsCredentialsW")
	procCredUnpack                     = winapi.NewLazySystemDLL("credui.dll").NewProc("CredUnPackAuthenticationBufferW")
	procCoTaskMemFree                  = winapi.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree")
)

type credUIInfo struct {
	Size        uint32
	Parent      uintptr
	MessageText *uint16
	CaptionText *uint16
	Banner      uintptr
}

func PromptForSystemCredentials() (coreservice.SystemCredentials, error) {
	return PromptForSystemCredentialsWithMessage("结束保护需要确认 Windows 系统凭据。")
}

func PromptForSystemCredentialsWithMessage(messageText string) (coreservice.SystemCredentials, error) {
	if strings.TrimSpace(messageText) == "" {
		return coreservice.SystemCredentials{}, ErrSystemCredentialInvalid
	}
	caption, err := winapi.UTF16PtrFromString("Desktop Guard Pro")
	if err != nil {
		return coreservice.SystemCredentials{}, err
	}
	message, err := winapi.UTF16PtrFromString(messageText)
	if err != nil {
		return coreservice.SystemCredentials{}, err
	}
	info := credUIInfo{
		Size: uint32(unsafe.Sizeof(credUIInfo{})), CaptionText: caption, MessageText: message,
	}
	var authPackage, outputSize uint32
	var output unsafe.Pointer
	result, _, callErr := procCredUIPrompt.Call(
		uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&authPackage)), 0, 0,
		uintptr(unsafe.Pointer(&output)), uintptr(unsafe.Pointer(&outputSize)), 0, endSessionCredentialPromptFlags,
	)
	if result != 0 {
		if result == windowsErrorCancelled {
			return coreservice.SystemCredentials{}, ErrSystemCredentialPromptCancelled
		}
		if callErr != nil {
			return coreservice.SystemCredentials{}, callErr
		}
		return coreservice.SystemCredentials{}, fmt.Errorf("show Windows credential prompt: %d", result)
	}
	defer func() {
		clear(unsafe.Slice((*byte)(output), int(outputSize)))
		procCoTaskMemFree.Call(uintptr(output))
	}()
	return unpackSystemCredentials(output, outputSize)
}

func unpackSystemCredentials(buffer unsafe.Pointer, size uint32) (coreservice.SystemCredentials, error) {
	var userLength, domainLength, passwordLength uint32
	result, _, callErr := procCredUnpack.Call(
		0, uintptr(buffer), uintptr(size), 0, uintptr(unsafe.Pointer(&userLength)),
		0, uintptr(unsafe.Pointer(&domainLength)), 0, uintptr(unsafe.Pointer(&passwordLength)),
	)
	if result == 0 && !errors.Is(callErr, syscall.Errno(windowsErrorInsufficientData)) {
		if callErr != nil {
			return coreservice.SystemCredentials{}, callErr
		}
		return coreservice.SystemCredentials{}, ErrSystemCredentialInvalid
	}
	if userLength == 0 || passwordLength == 0 {
		return coreservice.SystemCredentials{}, ErrSystemCredentialInvalid
	}
	user := make([]uint16, userLength)
	domain := make([]uint16, max(domainLength, 1))
	password := make([]uint16, passwordLength)
	defer clear(user)
	defer clear(domain)
	defer clear(password)
	result, _, callErr = procCredUnpack.Call(
		0, uintptr(buffer), uintptr(size), uintptr(unsafe.Pointer(&user[0])), uintptr(unsafe.Pointer(&userLength)),
		uintptr(unsafe.Pointer(&domain[0])), uintptr(unsafe.Pointer(&domainLength)),
		uintptr(unsafe.Pointer(&password[0])), uintptr(unsafe.Pointer(&passwordLength)),
	)
	if result == 0 {
		if callErr != nil {
			return coreservice.SystemCredentials{}, callErr
		}
		return coreservice.SystemCredentials{}, ErrSystemCredentialInvalid
	}
	credentials := coreservice.SystemCredentials{
		UserName: winapi.UTF16ToString(user), Domain: winapi.UTF16ToString(domain),
		Password: []byte(winapi.UTF16ToString(password)),
	}
	if credentials.UserName == "" || len(credentials.Password) == 0 {
		clear(credentials.Password)
		return coreservice.SystemCredentials{}, ErrSystemCredentialInvalid
	}
	return credentials, nil
}
