package api

import (
	"net/http"
	"time"

	"sas/store"
)

type manifestResponse struct {
	AssetID     string          `json:"asset_id"`
	Segments    []store.Segment `json:"segments"`
	LastUpdated time.Time       `json:"last_updated"`
}

func (a *AssetAPI) Manifest(w http.ResponseWriter, r *http.Request) {
	assetID := r.PathValue("asset_id")

	segments, lastUpdated, ok := a.store.Get(assetID)
	if !ok {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}

	writeJSON(w, http.StatusOK, manifestResponse{
		AssetID:     assetID,
		Segments:    segments,
		LastUpdated: lastUpdated,
	})
}
