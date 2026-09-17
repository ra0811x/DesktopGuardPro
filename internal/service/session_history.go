package service

import (
	"context"
	"errors"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/storage"
)

type SessionListRequest struct {
	Cursor string `json:"cursor,omitempty"`
}

func (api *API) listSessions(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	var payload SessionListRequest
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session history request is invalid")
	}
	store, ok := api.store.(interface {
		ListSessions(context.Context, string) (storage.SessionListPage, error)
	})
	if !ok {
		return api.errorResponse(request, ErrorCodeAnalysisUnavailable, "session history is unavailable")
	}
	page, err := store.ListSessions(ctx, payload.Cursor)
	if errors.Is(err, storage.ErrSessionHistoryCursor) {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session history cursor is invalid")
	}
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "cannot read session history")
	}
	return api.response(request, contracts.MessageTypeSessionListResult, page)
}
