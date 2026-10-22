package api

import "net/http"

type indexResponse struct {
	Service   string   `json:"service"`
	Endpoints []string `json:"endpoints"`
}

func (a *AssetAPI) Index(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, indexResponse{
		Service:   "streaming-availability-service",
		Endpoints: []string{"POST /ingest", "GET /manifest/{asset_id}", "GET /health"},
	})
}
