package agent

import (
	"errors"
	"sync"
	"time"
	"unsafe"

	"desktopguardpro/internal/domain"
	coreservice "desktopguardpro/internal/service"
	platformwindows "desktopguardpro/internal/windows"
	"golang.org/x/sys/windows"
)

const (
	inputShieldPromptCancelled    = 1223
	inputShieldPromptGeneric      = 0x00040000
	inputShieldPromptDoNotPersist = 0x00000002
	inputShieldPromptExcludeCerts = 0x00000008
	inputShieldPromptAlwaysShow   = 0x00000080
	inputShieldPromptKeepUserName = 0x00100000
	inputShieldPromptFlags        = inputShieldPromptGeneric | inputShieldPromptDoNotPersist |
		inputShieldPromptExcludeCerts | inputShieldPromptAlwaysShow | inputShieldPromptKeepUserName
	inputShieldMessageYesNoCancel  = 0x00000003
	inputShieldMessageIconQuestion = 0x00000020
	inputShieldMessageTopmost      = 0x00040000
	inputShieldMessageResultYes    = 6
	inputShieldMessageResultNo     = 7
)

var (
	inputShieldCredUI              = windows.NewLazySystemDLL("credui.dll").NewProc("CredUIPromptForCredentialsW")
	inputShieldMessageBox          = user32.NewProc("MessageBoxW")
	inputShieldGetWindowProcessID  = user32.NewProc("GetWindowThreadProcessId")
	inputShieldGetCurrentProcessID = inputShieldKernel32.NewProc("GetCurrentProcessId")
)

type inputShieldPromptInfo struct {
	Size        uint32
	Parent      uintptr
	MessageText *uint16
	CaptionText *uint16
	Banner      uintptr
}

var ErrInputShieldPromptCancelled = errors.New("input shield credential prompt was cancelled")

const inputShieldSystemCredentialPromptMessage = "解除本地输入防护需要确认 Windows 所有者凭据。验证期间，其他窗口仍保持输入阻断。"

func promptInputShieldCredentials(
	shield *windowsInputShield,
	policy domain.InputShieldPolicy,
) (coreservice.InputShieldCredentialVerifyRequest, error) {
	if shield == nil {
		return coreservice.InputShieldCredentialVerifyRequest{}, ErrInputShieldPromptCancelled
	}
	var request coreservice.InputShieldCredentialVerifyRequest
	err := withInputShieldVerificationWindow(shield, func() error {
		if policy.CredentialMode == domain.InputShieldCredentialWindows {
			credentials, err := platformwindows.PromptForSystemCredentialsWithMessage(inputShieldSystemCredentialPromptMessage)
			if err != nil {
				return err
			}
			request = coreservice.InputShieldCredentialVerifyRequest{
				UserName: credentials.UserName, Domain: credentials.Domain, Password: credentials.Password,
			}
			return nil
		}
		recovery := false
		if policy.AllowRecoveryCode {
			choice, err := chooseInputShieldLocalCredential()
			if err != nil {
				return err
			}
			recovery = choice
		}
		secret, err := promptInputShieldLocalSecret(recovery)
		if err != nil {
			return err
		}
		request = coreservice.InputShieldCredentialVerifyRequest{Password: secret, Recovery: recovery}
		return nil
	})
	if err != nil {
		clear(request.Password)
		return coreservice.InputShieldCredentialVerifyRequest{}, err
	}
	return request, nil
}

func withInputShieldVerificationWindow(shield *windowsInputShield, prompt func() error) error {
	done := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		currentProcess, _, _ := inputShieldGetCurrentProcessID.Call()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				foreground := uintptr(windows.GetForegroundWindow())
				if inputShieldVerificationWindowCandidate(foreground, currentProcess) {
					shield.SetVerificationWindow(foreground)
				}
			}
		}
	}()
	err := prompt()
	close(done)
	wait.Wait()
	shield.SetVerificationWindow(0)
	return err
}

func inputShieldVerificationWindowCandidate(window, currentProcess uintptr) bool {
	if window == 0 || currentProcess == 0 {
		return false
	}
	var processID uint32
	inputShieldGetWindowProcessID.Call(window, uintptr(unsafe.Pointer(&processID)))
	return uintptr(processID) == currentProcess
}

func chooseInputShieldLocalCredential() (bool, error) {
	caption, _ := windows.UTF16PtrFromString("Desktop Guard Pro")
	message, _ := windows.UTF16PtrFromString("请选择本地输入防护的验证方式。\n\n是：使用一次性恢复码\n否：使用本地防护密码\n取消：返回保护状态")
	result, _, _ := inputShieldMessageBox.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(caption)),
		inputShieldMessageYesNoCancel|inputShieldMessageIconQuestion|inputShieldMessageTopmost)
	switch result {
	case inputShieldMessageResultYes:
		return true, nil
	case inputShieldMessageResultNo:
		return false, nil
	default:
		return false, ErrInputShieldPromptCancelled
	}
}

func promptInputShieldLocalSecret(recovery bool) ([]byte, error) {
	caption, _ := windows.UTF16PtrFromString("Desktop Guard Pro")
	messageText := "请输入本地输入防护密码。"
	if recovery {
		messageText = "请输入一次性恢复码。验证成功后，该恢复码立即失效。"
	}
	message, _ := windows.UTF16PtrFromString(messageText)
	info := inputShieldPromptInfo{
		Size: uint32(unsafe.Sizeof(inputShieldPromptInfo{})), CaptionText: caption, MessageText: message,
	}
	userName := make([]uint16, 64)
	copy(userName, windows.StringToUTF16("DesktopGuardPro"))
	password := make([]uint16, 256)
	defer clear(userName)
	defer clear(password)
	var save int32
	result, _, callErr := inputShieldCredUI.Call(
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(caption)), 0, 0,
		uintptr(unsafe.Pointer(&userName[0])), uintptr(len(userName)),
		uintptr(unsafe.Pointer(&password[0])), uintptr(len(password)),
		uintptr(unsafe.Pointer(&save)), inputShieldPromptFlags,
	)
	if result != 0 {
		if result == inputShieldPromptCancelled {
			return nil, ErrInputShieldPromptCancelled
		}
		if callErr != nil {
			return nil, callErr
		}
		return nil, errors.New("show input shield local credential prompt")
	}
	secret := []byte(windows.UTF16ToString(password))
	if len(secret) == 0 {
		return nil, coreservice.ErrInputShieldCredentialInvalid
	}
	return secret, nil
}
