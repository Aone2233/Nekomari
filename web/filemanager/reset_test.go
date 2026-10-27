package filemanager

import (
	"io"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestResetClientOperationsRetiresRepliesStreamsAndPreviewGrants(t *testing.T) {
	const id = "reset-files"
	wait, other := make(chan v2.FileResult, 1), make(chan v2.FileResult, 1)
	pendingMu.Lock()
	pending["retired-reply"], pending["other-reply"] = pendingCall{uuid: id, response: wait}, pendingCall{uuid: "other", response: other}
	pendingMu.Unlock()
	t.Cleanup(func() { removePending("retired-reply"); removePending("other-reply") })
	transfer, err := newStreamTransfer(t.Context(), id, streamUploadDirection, 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transfer.close(nil) })
	previewTokenMu.Lock()
	previewTokens["retired-preview"], previewTokens["other-preview"] = previewToken{ClientUUID: id}, previewToken{ClientUUID: "other"}
	previewTokenMu.Unlock()
	t.Cleanup(func() {
		previewTokenMu.Lock()
		delete(previewTokens, "retired-preview")
		delete(previewTokens, "other-preview")
		previewTokenMu.Unlock()
	})
	readDone := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, transfer.reader); readDone <- err }()
	ResetClientOperations(id)
	select {
	case result := <-wait:
		if result.OK || result.Error != "client credential reset" {
			t.Fatal("pending file operation did not receive explicit reset")
		}
	default:
		t.Fatal("pending file operation was not woken")
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("reset stream ended as a successful transfer")
		}
	case <-time.After(time.Second):
		t.Fatal("reset stream remained blocked")
	}
	if err := transfer.markConnected(); err == nil {
		t.Fatal("reset transfer admitted a late agent")
	}
	if Resolve(v2.FileResult{UUID: id, RequestID: "retired-reply", OK: true}) {
		t.Fatal("late retired reply resolved")
	}
	if !Resolve(v2.FileResult{UUID: "other", RequestID: "other-reply", OK: true}) {
		t.Fatal("reset canceled another node's request")
	}
	previewTokenMu.Lock()
	_, retired := previewTokens["retired-preview"]
	_, retained := previewTokens["other-preview"]
	previewTokenMu.Unlock()
	if retired || !retained {
		t.Fatal("preview reset did not isolate node grants")
	}
}
