package api

import (
	"net/http"
	"time"
)

type healthResponse struct {
	Status        string `json:"status"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

func (a *AssetAPI) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(a.started).Seconds()),
	})
}
