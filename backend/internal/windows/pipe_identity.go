package windows

import (
	"errors"
	"fmt"
	"net"

	winapi "golang.org/x/sys/windows"
)

const maximumClientImagePathCharacters = 32 * 1024

var ErrPipeClientIdentityUnavailable = errors.New("named pipe client identity is unavailable")

type PipeClientIdentity struct {
	ProcessID        uint32
	WindowsSessionID uint32
	UserSID          string
	ImagePath        string
}

type handleConnection interface {
	Fd() uintptr
}

func IdentifyPipeClient(connection net.Conn) (PipeClientIdentity, error) {
	handleSource, ok := connection.(handleConnection)
	if !ok || handleSource.Fd() == 0 {
		return PipeClientIdentity{}, ErrPipeClientIdentityUnavailable
	}
	var processID uint32
	if err := winapi.GetNamedPipeClientProcessId(winapi.Handle(handleSource.Fd()), &processID); err != nil || processID == 0 {
		return PipeClientIdentity{}, fmt.Errorf("%w: read client process id: %v", ErrPipeClientIdentityUnavailable, err)
	}
	process, err := winapi.OpenProcess(winapi.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return PipeClientIdentity{}, fmt.Errorf("%w: open client process: %v", ErrPipeClientIdentityUnavailable, err)
	}
	defer winapi.CloseHandle(process)

	var token winapi.Token
	if err := winapi.OpenProcessToken(process, winapi.TOKEN_QUERY, &token); err != nil {
		return PipeClientIdentity{}, fmt.Errorf("%w: open client token: %v", ErrPipeClientIdentityUnavailable, err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return PipeClientIdentity{}, fmt.Errorf("%w: read client user SID: %v", ErrPipeClientIdentityUnavailable, err)
	}
	var sessionID uint32
	if err := winapi.ProcessIdToSessionId(processID, &sessionID); err != nil {
		return PipeClientIdentity{}, fmt.Errorf("%w: read client session id: %v", ErrPipeClientIdentityUnavailable, err)
	}
	buffer := make([]uint16, maximumClientImagePathCharacters)
	length := uint32(len(buffer))
	if err := winapi.QueryFullProcessImageName(process, 0, &buffer[0], &length); err != nil || length == 0 {
		return PipeClientIdentity{}, fmt.Errorf("%w: read client image path: %v", ErrPipeClientIdentityUnavailable, err)
	}
	return PipeClientIdentity{
		ProcessID: processID, WindowsSessionID: sessionID,
		UserSID: user.User.Sid.String(), ImagePath: winapi.UTF16ToString(buffer[:length]),
	}, nil
}
