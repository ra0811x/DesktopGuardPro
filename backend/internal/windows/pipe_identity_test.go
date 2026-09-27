package windows

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	winapi "golang.org/x/sys/windows"
)

func TestIdentifyPipeClientReadsProcessTokenAndSession(t *testing.T) {
	pipePath := fmt.Sprintf(`\\.\pipe\DesktopGuardPro.Identity.%d.%d`, os.Getpid(), time.Now().UnixNano())
	listener, err := ListenPipe(pipePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	identityResult := make(chan struct {
		identity PipeClientIdentity
		err      error
	}, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			identityResult <- struct {
				identity PipeClientIdentity
				err      error
			}{err: acceptErr}
			return
		}
		defer connection.Close()
		identity, identifyErr := IdentifyPipeClient(connection)
		identityResult <- struct {
			identity PipeClientIdentity
			err      error
		}{identity: identity, err: identifyErr}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := DialPipe(ctx, pipePath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result := <-identityResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.identity.ProcessID != uint32(os.Getpid()) || result.identity.UserSID != currentTokenUserSID(t).String() {
		t.Fatalf("unexpected client identity: %+v", result.identity)
	}
	var wantSessionID uint32
	if err := winapi.ProcessIdToSessionId(uint32(os.Getpid()), &wantSessionID); err != nil {
		t.Fatal(err)
	}
	if result.identity.WindowsSessionID != wantSessionID {
		t.Fatalf("session id = %d, want %d", result.identity.WindowsSessionID, wantSessionID)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(result.identity.ImagePath), filepath.Clean(executable)) {
		t.Fatalf("image path = %q, want %q", result.identity.ImagePath, executable)
	}
}

func TestIdentifyPipeClientRejectsConnectionWithoutWindowsHandle(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	if _, err := IdentifyPipeClient(server); err != ErrPipeClientIdentityUnavailable {
		t.Fatalf("expected unavailable identity, got %v", err)
	}
}
