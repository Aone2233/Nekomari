package upload

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

const maxChunkRequestSize = ChunkSize + 1*1024*1024

type Result struct {
	Message string
	Data    any
}

type Finalizer func(Session) (Result, error)

type Handler struct {
	Store      *Store
	Finalizers map[Purpose]Finalizer
}

func NewHandler(store *Store, finalizers map[Purpose]Finalizer) *Handler {
	return &Handler{Store: store, Finalizers: finalizers}
}

func (h *Handler) Init(c *gin.Context) {
	var request struct {
		Purpose  Purpose `json:"purpose" binding:"required"`
		Size     int64   `json:"size" binding:"required"`
		Filename string  `json:"filename"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if _, ok := h.Finalizers[request.Purpose]; !ok {
		api.RespondError(c, http.StatusBadRequest, "unsupported upload purpose")
		return
	}
	session, err := h.Store.Init(request.Purpose, request.Filename, request.Size)
	if err != nil {
		h.respondUploadError(c, err)
		return
	}
	api.RespondSuccess(c, gin.H{
		"upload_id":  session.ID,
		"chunk_size": ChunkSize,
	})
}

func (h *Handler) Chunk(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxChunkRequestSize)
	uploadID := c.PostForm("upload_id")
	index, err := strconv.ParseInt(c.PostForm("chunk_index"), 10, 64)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "chunk_index must be an integer")
		return
	}
	chunk, _, err := c.Request.FormFile("chunk_data")
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("get chunk data: %v", err))
		return
	}
	defer chunk.Close()
	if err := h.Store.SaveChunk(uploadID, index, chunk); err != nil {
		h.respondUploadError(c, err)
		return
	}
	api.RespondSuccess(c, gin.H{"received": true, "chunk_index": index})
}

func (h *Handler) Merge(c *gin.Context) {
	var request struct {
		UploadID string `json:"upload_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	result, err := h.Store.Complete(request.UploadID, func(session Session) (Result, error) {
		finalize, ok := h.Finalizers[session.Metadata.Purpose]
		if !ok {
			return Result{}, fmt.Errorf("unsupported upload purpose")
		}
		return finalize(session)
	})
	if err != nil {
		h.respondUploadError(c, err)
		return
	}
	api.RespondSuccessMessage(c, result.Message, result.Data)
}

func (h *Handler) Cancel(c *gin.Context) {
	var request struct {
		UploadID string `json:"upload_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if err := h.Store.Cancel(request.UploadID); err != nil {
		h.respondUploadError(c, err)
		return
	}
	api.RespondSuccess(c, gin.H{})
}

func (h *Handler) respondUploadError(c *gin.Context, err error) {
	if errors.Is(err, ErrBusy) || errors.Is(err, ErrQuota) {
		c.Header("Retry-After", "5")
		api.RespondError(c, http.StatusTooManyRequests, err.Error())
		return
	}
	if errors.Is(err, ErrNotFound) {
		api.RespondError(c, http.StatusNotFound, "upload not found or expired")
		return
	}
	// 507 is the status that says "the server cannot store this", so a full
	// disk is not reported as a malformed request; the message carries the
	// measured free space and the required budget. No Retry-After is sent: the
	// condition clears when an admin frees space or a session expires, not on a
	// short timer.
	if errors.Is(err, ErrNoSpace) {
		api.RespondError(c, http.StatusInsufficientStorage, err.Error())
		return
	}
	api.RespondError(c, http.StatusBadRequest, err.Error())
}
