package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"sas/store"
)

type AssetAPI struct {
	store   *store.Store
	logger  *slog.Logger
	started time.Time
}

func New(s *store.Store, logger *slog.Logger) *AssetAPI {
	return &AssetAPI{store: s, logger: logger, started: time.Now()}
}

func (a *AssetAPI) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.Index)
	mux.HandleFunc("POST /ingest", a.Ingest)
	mux.HandleFunc("GET /manifest/{asset_id...}", a.Manifest)
	mux.HandleFunc("GET /health", a.Health)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
