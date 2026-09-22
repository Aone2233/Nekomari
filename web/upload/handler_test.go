package upload

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIncompleteMergeCanResume(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: ChunkSize * 2, FreeSpace: unlimitedFreeSpace}
	session, err := store.Init(PurposeTheme, "theme.zip", ChunkSize+1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveChunk(session.ID, 0, bytes.NewReader(make([]byte, ChunkSize))); err != nil {
		t.Fatal(err)
	}
	finalized := false
	h := NewHandler(store, map[Purpose]Finalizer{PurposeTheme: func(s Session) (Result, error) {
		finalized = true
		info, err := os.Stat(s.ArchivePath)
		if err != nil || info.Size() != ChunkSize+1 {
			t.Fatalf("invalid merged archive: %v", err)
		}
		return Result{Message: "ok"}, nil
	}})
	r := gin.New()
	r.POST("/merge", h.Merge)
	merge := func() int {
		req := httptest.NewRequest("POST", "/merge", bytes.NewBufferString(`{"upload_id":"`+session.ID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if status := merge(); status != http.StatusBadRequest || finalized {
		t.Fatalf("incomplete merge: %d", status)
	}
	if err := store.SaveChunk(session.ID, 1, bytes.NewReader([]byte{1})); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if status := merge(); status != http.StatusOK || !finalized {
		t.Fatalf("completed merge: %d", status)
	}
	if _, err := os.Stat(session.Directory); !os.IsNotExist(err) {
		t.Fatal("completed upload not cleaned up")
	}
}

// TestInitReportsFullDiskAsInsufficientStorage keeps a full disk from reading
// like a malformed request: the client and the operator get the storage status
// and the measured numbers.
func TestInitReportsFullDiskAsInsufficientStorage(t *testing.T) {
	store := &Store{
		Root:      t.TempDir(),
		MaxSize:   1 << 20,
		FreeSpace: func(string) (int64, error) { return 1024, nil },
	}
	h := NewHandler(store, map[Purpose]Finalizer{PurposeTheme: func(Session) (Result, error) {
		return Result{Message: "ok"}, nil
	}})
	r := gin.New()
	r.POST("/init", h.Init)

	body := bytes.NewBufferString(`{"purpose":"theme","size":1048576,"filename":"theme.zip"}`)
	req := httptest.NewRequest("POST", "/init", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInsufficientStorage)
	}
	if !strings.Contains(w.Body.String(), "insufficient free disk space") {
		t.Fatalf("response does not explain the refusal: %s", w.Body.String())
	}
}
