package service

import (
	"context"
	"testing"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/storage"
)

type historyAPIStore struct {
	testSessionStore
	calls  int
	cursor string
}

func (store *historyAPIStore) ListSessions(ctx context.Context, cursor string) (storage.SessionListPage, error) {
	store.calls++
	store.cursor = cursor
	return storage.SessionListPage{Items: []storage.SessionListItem{{Session: domain.Session{ID: "old-session", Name: "已完成保护", State: domain.SessionStateCompleted, Revision: 4}}}}, nil
}

func TestSessionHistoryRequiresOwnerAndWorksWithoutCurrentSession(t *testing.T) {
	store := &historyAPIStore{}
	api, now := newTestAPI()
	api.store, api.authorizedUserSID = store, "S-1-5-21-owner"
	request := newTestMessage(t, contracts.MessageTypeSessionList, now, map[string]string{"cursor": "25"})
	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "S-1-5-21-other"})
	if err != nil || store.calls != 0 {
		t.Fatalf("unauthorized history read: %v", err)
	}
	var denied ErrorResult
	decodeTestPayload(t, response, &denied)
	if denied.Code != ErrorCodeUnauthorized {
		t.Fatalf("response=%s", response.Payload)
	}
	response, err = api.HandleForClient(request, ClientIdentity{UserSID: api.authorizedUserSID})
	if err != nil || response.Type != contracts.MessageTypeSessionListResult || store.calls != 1 || store.cursor != "25" {
		t.Fatalf("history=%s calls=%d error=%v", response.Payload, store.calls, err)
	}
	var page storage.SessionListPage
	decodeTestPayload(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].Session.ID != "old-session" {
		t.Fatalf("history=%+v", page)
	}
}
