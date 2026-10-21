package api

import (
	"encoding/json"
	"net/http"
	"time"

	"sas/store"
)

const maxBodyBytes = 1 << 20

type ingestRequest struct {
	AssetID      string    `json:"asset_id"`
	SegmentIndex *int      `json:"segment_index"`
	CDNURL       string    `json:"cdn_url"`
	ReceivedAt   time.Time `json:"received_at"`
}

func (req ingestRequest) missingField() string {
	switch {
	case req.AssetID == "":
		return "asset_id"
	case req.SegmentIndex == nil:
		return "segment_index"
	case req.CDNURL == "":
		return "cdn_url"
	case req.ReceivedAt.IsZero():
		return "received_at"
	}
	return ""
}

func (a *AssetAPI) Ingest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.logger.Warn("ingest rejected", "reason", "decode", "error", err)
		writeError(w, http.StatusBadRequest, "Could not decode request: JSON parsing failed")
		return
	}

	if field := req.missingField(); field != "" {
		a.logger.Warn("ingest rejected", "reason", "missing field", "field", field)
		writeError(w, http.StatusBadRequest, "missing required field: "+field)
		return
	}

	seg := store.Segment{Index: *req.SegmentIndex, CDNURL: req.CDNURL}
	a.store.Put(req.AssetID, seg, req.ReceivedAt)

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
