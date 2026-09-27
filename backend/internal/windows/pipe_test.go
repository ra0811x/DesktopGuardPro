package windows

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/ipc"
)

func TestNamedPipeMessageRoundTrip(t *testing.T) {
	t.Parallel()

	pipePath := fmt.Sprintf(
		`\\.\pipe\DesktopGuardPro.Test.%d.%d`,
		os.Getpid(),
		time.Now().UnixNano(),
	)
	listener, err := ListenPipe(pipePath)
	if err != nil {
		t.Fatalf("ListenPipe() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()

		request, readErr := ipc.ReadMessage(connection)
		if readErr != nil {
			serverDone <- readErr
			return
		}
		response, createErr := contracts.NewMessage(
			request.RequestID,
			contracts.MessageTypeHealthResult,
			time.Now().UTC().Add(time.Second),
			map[string]string{"status": "running"},
		)
		if createErr != nil {
			serverDone <- createErr
			return
		}
		serverDone <- ipc.WriteMessage(connection, response)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connection, err := DialPipe(ctx, pipePath)
	if err != nil {
		t.Fatalf("DialPipe() error = %v", err)
	}
	defer connection.Close()

	request, err := contracts.NewMessage(
		"request-1",
		contracts.MessageTypeHealthGet,
		time.Now().UTC().Add(time.Second),
		map[string]string{"scope": "service"},
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := ipc.WriteMessage(connection, request); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	response, err := ipc.ReadMessage(connection)
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if response.RequestID != request.RequestID {
		t.Fatalf("response request id = %q, want %q", response.RequestID, request.RequestID)
	}
	if response.Type != contracts.MessageTypeHealthResult {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeHealthResult)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("pipe server error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pipe server did not finish")
	}
}
