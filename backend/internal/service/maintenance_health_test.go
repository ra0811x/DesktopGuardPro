package service

import (
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
)

func TestLocalSystemCanProbeHealthWithoutSessionDataOrControl(t *testing.T) {
	api, now := newTestAPI()
	api.authorizedUserSID = "S-1-5-21-1000-1001-1002-1003"
	if _, err := api.coordinator.Create("private-session", "Private name"); err != nil {
		t.Fatal(err)
	}
	for _, messageType := range []contracts.MessageType{contracts.MessageTypeHealthGet, contracts.MessageTypeSessionCurrentGet, contracts.MessageTypeSessionCreate} {
		response, err := api.HandleForClient(newTestMessage(t, messageType, now, struct{}{}), ClientIdentity{UserSID: "S-1-5-18"})
		if err != nil {
			t.Fatal(err)
		}
		if messageType == contracts.MessageTypeHealthGet {
			if response.Type != contracts.MessageTypeHealthResult {
				t.Fatalf("installer health rejected: %s", response.Payload)
			}
			var health HealthResult
			decodeTestPayload(t, response, &health)
			if health.Status != HealthStatusRunning || health.Session != nil || !health.MaintenanceGate {
				t.Fatalf("unexpected privileged health: %+v", health)
			}
		} else {
			var failure ErrorResult
			decodeTestPayload(t, response, &failure)
			if failure.Code != ErrorCodeUnauthorized {
				t.Fatalf("system maintenance gained control: %s", response.Payload)
			}
		}
	}
}

func TestSystemHealthDoesNotWaitForSessionPersistence(t *testing.T) {
	api, now := newTestAPI()
	api.authorizedUserSID = "S-1-5-21-1000"
	api.coordinator.mu.Lock()
	defer api.coordinator.mu.Unlock()
	done := make(chan error, 1)
	request := newTestMessage(t, contracts.MessageTypeHealthGet, now, struct{}{})
	go func() { _, err := api.HandleForClient(request, ClientIdentity{UserSID: "S-1-5-18"}); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("MSI health waits for a session that may be waiting for the maintenance lock")
	}
}
