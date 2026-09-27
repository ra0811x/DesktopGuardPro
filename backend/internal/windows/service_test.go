package windows

import (
	"testing"
	"time"

	coreservice "desktopguardpro/internal/service"

	"golang.org/x/sys/windows/svc"
)

func TestServiceHandlerLifecycle(t *testing.T) {
	t.Parallel()

	requests := make(chan svc.ChangeRequest, 5)
	statuses := make(chan svc.Status, 8)
	done := make(chan serviceResult, 1)
	handler := NewServiceHandler(coreservice.NewCoordinator())
	powerEvents := make(chan uint32, 1)
	handler.SetPowerEventHandler(func(eventType uint32) { powerEvents <- eventType })

	go func() {
		specific, exitCode := handler.Execute(nil, requests, statuses)
		done <- serviceResult{specific: specific, exitCode: exitCode}
	}()

	assertServiceState(t, statuses, svc.StartPending)
	running := assertServiceState(t, statuses, svc.Running)
	wantAccepts := svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent
	if running.Accepts != wantAccepts {
		t.Fatalf("running accepts = %v, want %v", running.Accepts, wantAccepts)
	}

	requests <- svc.ChangeRequest{Cmd: svc.Pause}
	requests <- svc.ChangeRequest{Cmd: svc.Continue}
	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: 0x12}
	if eventType := <-powerEvents; eventType != 0x12 {
		t.Fatalf("power event = %#x", eventType)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
	assertServiceState(t, statuses, svc.Running)

	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	assertServiceState(t, statuses, svc.StopPending)

	select {
	case result := <-done:
		if result.specific {
			t.Fatal("Execute() specific exit code = true, want false")
		}
		if result.exitCode != 0 {
			t.Fatalf("Execute() exit code = %d, want 0", result.exitCode)
		}
	case <-time.After(time.Second):
		t.Fatal("Execute() did not stop")
	}
}

type serviceResult struct {
	specific bool
	exitCode uint32
}

func assertServiceState(t *testing.T, statuses <-chan svc.Status, want svc.State) svc.Status {
	t.Helper()

	select {
	case status := <-statuses:
		if status.State != want {
			t.Fatalf("service state = %v, want %v", status.State, want)
		}
		return status
	case <-time.After(time.Second):
		t.Fatalf("service did not report state %v", want)
		return svc.Status{}
	}
}
