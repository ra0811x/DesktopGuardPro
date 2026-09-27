package appbridge

import (
	"desktopguardpro/internal/contracts"
	coreservice "desktopguardpro/internal/service"
	"desktopguardpro/internal/storage"
)

func (bridge *Bridge) ListSessions(cursor string) (storage.SessionListPage, error) {
	var result storage.SessionListPage
	err := bridge.call(contracts.MessageTypeSessionList, coreservice.SessionListRequest{Cursor: cursor}, contracts.MessageTypeSessionListResult, &result)
	return result, err
}
