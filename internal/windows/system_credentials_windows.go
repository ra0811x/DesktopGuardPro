package windows

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	coreservice "desktopguardpro/internal/service"

	winapi "golang.org/x/sys/windows"
)

const (
	logonTypeInteractive = 2
	logonProviderDefault = 0
)

var (
	ErrSystemCredentialInvalid          = errors.New("system credentials are invalid")
	ErrSystemCredentialIdentityMismatch = errors.New("system credential identity does not match the installation owner")
	procLogonUserW                      = winapi.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")
)

type authenticatedSystemToken interface {
	UserSID() (string, error)
	Close() error
}

type systemCredentialDependencies struct {
	logon func(userName, domain string, password []byte) (authenticatedSystemToken, error)
}

type SystemCredentialVerifier struct {
	dependencies systemCredentialDependencies
}

func NewSystemCredentialVerifier() *SystemCredentialVerifier {
	return &SystemCredentialVerifier{dependencies: systemCredentialDependencies{logon: logonSystemUser}}
}

func (verifier *SystemCredentialVerifier) VerifySystemCredentials(
	ctx context.Context,
	credentials coreservice.SystemCredentials,
	expectedUserSID string,
) error {
	if ctx == nil || ctx.Err() != nil || strings.TrimSpace(credentials.UserName) == "" ||
		len(credentials.Password) == 0 || strings.TrimSpace(expectedUserSID) == "" {
		return ErrSystemCredentialInvalid
	}
	defer clear(credentials.Password)
	dependencies := verifier.dependencies
	if dependencies.logon == nil {
		dependencies.logon = logonSystemUser
	}
	token, err := dependencies.logon(credentials.UserName, credentials.Domain, credentials.Password)
	if err != nil {
		return fmt.Errorf("authenticate Windows credentials: %w", err)
	}
	defer token.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	actualUserSID, err := token.UserSID()
	if err != nil {
		return fmt.Errorf("read authenticated user SID: %w", err)
	}
	if !strings.EqualFold(actualUserSID, strings.TrimSpace(expectedUserSID)) {
		return ErrSystemCredentialIdentityMismatch
	}
	return nil
}

type windowsSystemToken struct{ token winapi.Token }

func (token windowsSystemToken) UserSID() (string, error) {
	user, err := token.token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func (token windowsSystemToken) Close() error {
	return token.token.Close()
}

func logonSystemUser(userName, domain string, password []byte) (authenticatedSystemToken, error) {
	userNameUTF16, err := winapi.UTF16PtrFromString(userName)
	if err != nil {
		return nil, err
	}
	domainUTF16, err := winapi.UTF16PtrFromString(domain)
	if err != nil {
		return nil, err
	}
	passwordUTF16, err := passwordUTF16Pointer(password)
	if err != nil {
		return nil, err
	}
	defer clear(passwordUTF16)
	var token winapi.Token
	result, _, callErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(userNameUTF16)),
		uintptr(unsafe.Pointer(domainUTF16)),
		uintptr(unsafe.Pointer(&passwordUTF16[0])),
		uintptr(logonTypeInteractive),
		uintptr(logonProviderDefault),
		uintptr(unsafe.Pointer(&token)),
	)
	if result == 0 {
		if callErr != nil {
			return nil, callErr
		}
		return nil, ErrSystemCredentialInvalid
	}
	return windowsSystemToken{token: token}, nil
}

func passwordUTF16Pointer(password []byte) ([]uint16, error) {
	if len(password) == 0 {
		return nil, ErrSystemCredentialInvalid
	}
	runes := make([]rune, 0, len(password))
	for len(password) > 0 {
		character, size := utf8.DecodeRune(password)
		if character == utf8.RuneError && size == 1 {
			return nil, ErrSystemCredentialInvalid
		}
		if character == 0 {
			return nil, ErrSystemCredentialInvalid
		}
		runes = append(runes, character)
		password = password[size:]
	}
	encoded := utf16.Encode(runes)
	clear(runes)
	return append(encoded, 0), nil
}
