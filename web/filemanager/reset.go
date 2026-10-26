package filemanager

import (
	"errors"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// ResetClientOperations retires pending replies, relays and preview grants.
// It performs no network or filesystem work while admission is held.
func ResetClientOperations(uuid string) {
	var waiting []pendingCall
	pendingMu.Lock()
	for id, call := range pending {
		if call.uuid == uuid {
			waiting = append(waiting, call)
			delete(pending, id)
		}
	}
	pendingMu.Unlock()
	for _, call := range waiting {
		call.response <- v2.FileResult{UUID: uuid, Error: "client credential reset"}
	}
	var transfers []*streamTransfer
	streamTransfersMu.Lock()
	for _, transfer := range streamTransfers {
		if transfer.clientUUID == uuid {
			transfers = append(transfers, transfer)
		}
	}
	streamTransfersMu.Unlock()
	for _, transfer := range transfers {
		transfer.close(errors.New("client credential reset"))
	}
	previewTokenMu.Lock()
	for token, grant := range previewTokens {
		if grant.ClientUUID == uuid {
			delete(previewTokens, token)
		}
	}
	previewTokenMu.Unlock()
	downloadChunkMu.Lock()
	delete(downloadChunkSizes, uuid)
	downloadChunkMu.Unlock()
}
