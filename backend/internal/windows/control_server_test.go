package windows

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/ipc"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
)

type disconnectTestStore struct{ started, canceled, release chan struct{} }

func (*disconnectTestStore) CreateSession(context.Context, domain.Session, time.Time) error {
	return nil
}
func (*disconnectTestStore) UpdateSession(context.Context, domain.Session, time.Time) error {
	return nil
}
func (*disconnectTestStore) GetSession(context.Context, string) (domain.Session, error) {
	return domain.Session{}, nil
}
func (*disconnectTestStore) ListEvents(context.Context, string) ([]storage.EventRecord, error) {
	return nil, nil
}
func (store *disconnectTestStore) QueryTimeline(ctx context.Context, _ storage.TimelineQuery) (storage.TimelinePage, error) {
	close(store.started)
	select {
	case <-ctx.Done():
		close(store.canceled)
		return storage.TimelinePage{}, ctx.Err()
	case <-store.release:
		return storage.TimelinePage{}, nil
	}
}

func TestControlDisconnectCancelsRunningAnalysis(t *testing.T) {
	store := &disconnectTestStore{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	defer close(store.release)
	api := coreservice.NewPersistentAPI(coreservice.NewCoordinator(), store)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _ = ServeControlConnection(context.Background(), server, api, PipeClientIdentity{}) }()
	request, err := contracts.NewMessage("cancel", contracts.MessageTypeTimelineQuery, time.Now().UTC().Add(30*time.Second), coreservice.TimelineQueryRequest{SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ipc.WriteMessage(client, request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	client.Close()
	select {
	case <-store.canceled:
	case <-time.After(time.Second):
		t.Fatal("disconnected client left query running")
	}
}

func TestControlServerHandlesHealthRequest(t *testing.T) {
	t.Parallel()

	pipePath := fmt.Sprintf(
		`\\.\pipe\DesktopGuardPro.ControlTest.%d.%d`,
		os.Getpid(),
		time.Now().UnixNano(),
	)
	listener, err := ListenPipe(pipePath)
	if err != nil {
		t.Fatalf("ListenPipe() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	api := coreservice.NewAPI(coreservice.NewCoordinator())
	go func() {
		serverDone <- ServeControl(ctx, listener, api)
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	connection, err := DialPipe(dialCtx, pipePath)
	if err != nil {
		cancel()
		_ = listener.Close()
		t.Fatalf("DialPipe() error = %v", err)
	}

	request, err := contracts.NewMessage(
		"request-1",
		contracts.MessageTypeHealthGet,
		time.Now().UTC().Add(time.Second),
		struct{}{},
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
	if response.Type != contracts.MessageTypeHealthResult {
		t.Fatalf("response type = %q, want %q", response.Type, contracts.MessageTypeHealthResult)
	}

	_ = connection.Close()
	cancel()
	_ = listener.Close()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("ServeControl() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeControl() did not stop")
	}
}

func TestServeControlConnectionUsesSuppliedIdentity(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	defer clientConnection.Close()
	defer serverConnection.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := coreservice.NewAPI(coreservice.NewCoordinator())
	done := make(chan error, 1)
	go func() {
		done <- ServeControlConnection(ctx, serverConnection, api, PipeClientIdentity{
			ProcessID: 10, WindowsSessionID: 2, UserSID: "S-1-5-21-test", ImagePath: `C:\Test\desktop-guard-ui.exe`,
		})
	}()
	request, err := contracts.NewMessage("identity-request", contracts.MessageTypeHealthGet, time.Now().UTC().Add(time.Second), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ipc.WriteMessage(clientConnection, request); err != nil {
		t.Fatal(err)
	}
	if _, err := ipc.ReadMessage(clientConnection); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = clientConnection.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("identity-aware control connection did not stop")
	}
}
