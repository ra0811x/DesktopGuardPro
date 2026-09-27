package appbridge

import (
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
)

func TestBridgeListsHistoricalSessionsByCursor(t *testing.T) {
	bridge := newBridge(testMessageDialer(func(request contracts.Message) (contracts.MessageType, any) {
		var payload coreservice.SessionListRequest
		if err := request.DecodePayload(&payload); err != nil {
			t.Fatal(err)
		}
		if request.Type != contracts.MessageTypeSessionList || payload.Cursor != "123" {
			t.Fatalf("history request=%+v", request)
		}
		return contracts.MessageTypeSessionListResult, storage.SessionListPage{Items: []storage.SessionListItem{}, HasMore: true, NextCursor: "73"}
	}), time.Now)
	result, err := bridge.ListSessions("123")
	if err != nil || !result.HasMore || result.NextCursor != "73" {
		t.Fatalf("history=%+v, %v", result, err)
	}
}
