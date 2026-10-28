package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
)

var panelHistoryReadSlots = make(chan struct{}, 2)

func pprofHistoryHandler(c *gin.Context) {
	values := c.QueryArray("hours")
	hours := 24
	if len(values) > 1 {
		api.RespondError(c, http.StatusBadRequest, "hours must be 1 or 24")
		return
	}
	if len(values) == 1 {
		switch values[0] {
		case "1":
			hours = 1
		case "24":
		default:
			api.RespondError(c, http.StatusBadRequest, "hours must be 1 or 24")
			return
		}
	}
	select {
	case panelHistoryReadSlots <- struct{}{}:
		defer func() { <-panelHistoryReadSlots }()
	default:
		api.RespondError(c, http.StatusServiceUnavailable, "panel history is busy; retry later")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	history, err := metricstore.ReadPanelHistory(ctx, hours, time.Now())
	if err != nil {
		api.RespondError(c, http.StatusServiceUnavailable, "panel history is unavailable")
		return
	}
	api.RespondSuccess(c, history)
}
