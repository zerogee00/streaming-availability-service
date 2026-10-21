package api_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"sas/api"
	"sas/store"
)

var baseTime = time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

type manifest struct {
	AssetID     string          `json:"asset_id"`
	Segments    []store.Segment `json:"segments"`
	LastUpdated time.Time       `json:"last_updated"`
}

func newHandler() http.Handler {
	return api.New(store.New(), slog.New(slog.DiscardHandler)).Routes()
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func segmentJSON(assetID string, index int) string {
	return fmt.Sprintf(
		`{"asset_id":%q,"segment_index":%d,"cdn_url":"https://cdn.example.com/%s/seg%d.ts","received_at":%q}`,
		assetID, index, assetID, index, baseTime.Add(time.Duration(index)*time.Second).Format(time.RFC3339),
	)
}

func ingest(t *testing.T, h http.Handler, assetID string, index int) {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/ingest", segmentJSON(assetID, index))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest %s/%d: got status %d, body %s", assetID, index, rec.Code, rec.Body)
	}
}

func getManifest(t *testing.T, h http.Handler, assetID string) manifest {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/manifest/"+assetID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest %s: got status %d, body %s", assetID, rec.Code, rec.Body)
	}
	var m manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

func indexes(m manifest) []int {
	out := make([]int, len(m.Segments))
	for i, s := range m.Segments {
		out[i] = s.Index
	}
	return out
}

func TestIngest(t *testing.T) {
	const decodeErr = `{"error":"Could not decode request: JSON parsing failed"}`

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{"valid segment", segmentJSON("show/a/season/1/ep1", 3), http.StatusAccepted, `{"status":"accepted"}`},
		{"segment index zero is valid", segmentJSON("show/a/season/1/ep1", 0), http.StatusAccepted, `{"status":"accepted"}`},
		{"invalid JSON", `{ "asset_id": "show/thunderbirds", "segment_index": ]]]`, http.StatusBadRequest, decodeErr},
		{"segment_index as string", `{"asset_id":"a","segment_index":"four","cdn_url":"u","received_at":"2024-05-01T10:00:00Z"}`, http.StatusBadRequest, decodeErr},
		{"received_at not RFC 3339", `{"asset_id":"a","segment_index":1,"cdn_url":"u","received_at":"yesterday"}`, http.StatusBadRequest, decodeErr},
		{"empty body", "", http.StatusBadRequest, decodeErr},
		{"missing asset_id", `{"segment_index":0,"cdn_url":"u","received_at":"2024-05-01T10:00:00Z"}`, http.StatusBadRequest, `{"error":"missing required field: asset_id"}`},
		{"missing segment_index", `{"asset_id":"a","cdn_url":"u","received_at":"2024-05-01T10:00:00Z"}`, http.StatusBadRequest, `{"error":"missing required field: segment_index"}`},
		{"null segment_index", `{"asset_id":"a","segment_index":null,"cdn_url":"u","received_at":"2024-05-01T10:00:00Z"}`, http.StatusBadRequest, `{"error":"missing required field: segment_index"}`},
		{"missing cdn_url", `{"asset_id":"a","segment_index":0,"received_at":"2024-05-01T10:00:00Z"}`, http.StatusBadRequest, `{"error":"missing required field: cdn_url"}`},
		{"missing received_at", `{"asset_id":"a","segment_index":0,"cdn_url":"u"}`, http.StatusBadRequest, `{"error":"missing required field: received_at"}`},
		{"body over size limit", `{"asset_id":"` + strings.Repeat("x", 2<<20) + `"}`, http.StatusBadRequest, decodeErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newHandler(), http.MethodPost, "/ingest", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body: got %s, want %s", got, tt.wantBody)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type: got %q, want application/json", got)
			}
		})
	}
}

func TestManifestOrdering(t *testing.T) {
	tests := []struct {
		name   string
		ingest []int
		want   []int
	}{
		{"sequential", []int{0, 1, 2}, []int{0, 1, 2}},
		{"out of order", []int{5, 2, 0, 3, 1, 4}, []int{0, 1, 2, 3, 4, 5}},
		{"duplicates", []int{0, 1, 0, 1, 2}, []int{0, 1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHandler()
			const assetID = "show/thunderbirds/season/1/ep3"
			for _, i := range tt.ingest {
				ingest(t, h, assetID, i)
			}

			m := getManifest(t, h, assetID)

			if m.AssetID != assetID {
				t.Errorf("asset_id: got %q, want %q", m.AssetID, assetID)
			}
			if got := indexes(m); !slices.Equal(got, tt.want) {
				t.Errorf("segments: got %v, want %v", got, tt.want)
			}
			wantLast := baseTime.Add(time.Duration(slices.Max(tt.ingest)) * time.Second)
			if !m.LastUpdated.Equal(wantLast) {
				t.Errorf("last_updated: got %v, want %v", m.LastUpdated, wantLast)
			}
		})
	}
}

func TestManifestIndependentAssets(t *testing.T) {
	h := newHandler()
	assets := []string{"show/thunderbirds/season/1/ep1", "show/scooby/season/1/ep4", "show/theoriginals/season/1/ep1"}

	for i := range 2 {
		for _, a := range assets {
			ingest(t, h, a, i)
		}
	}

	for _, a := range assets {
		t.Run(a, func(t *testing.T) {
			m := getManifest(t, h, a)
			if got := indexes(m); !slices.Equal(got, []int{0, 1}) {
				t.Errorf("segments: got %v, want [0 1]", got)
			}
			for _, s := range m.Segments {
				if !strings.Contains(s.CDNURL, a) {
					t.Errorf("segment %d has cdn_url %q from another asset", s.Index, s.CDNURL)
				}
			}
		})
	}
}

func TestManifestNotFound(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{"unknown asset", "/manifest/show/nonexistent/season/1/ep99"},
		{"empty asset id", "/manifest/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newHandler(), http.MethodGet, tt.target, "")

			if rec.Code != http.StatusNotFound {
				t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"asset not found"}` {
				t.Errorf("body: got %s", got)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	rec := do(t, newHandler(), http.MethodGet, "/health", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		Status        string `json:"status"`
		UptimeSeconds *int64 `json:"uptime_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status field: got %q, want ok", body.Status)
	}
	if body.UptimeSeconds == nil || *body.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds: got %v, want a non-negative number", body.UptimeSeconds)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	tests := []struct {
		method string
		target string
	}{
		{http.MethodGet, "/ingest"},
		{http.MethodPost, "/health"},
		{http.MethodDelete, "/manifest/show/a"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			rec := do(t, newHandler(), tt.method, tt.target, "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status: got %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestConcurrentIngestAndManifest(t *testing.T) {
	h := newHandler()
	const assets, segments = 20, 50

	var wg sync.WaitGroup
	for a := range assets {
		assetID := fmt.Sprintf("show/load/ep%d", a)
		for i := range segments {
			wg.Go(func() {
				if rec := do(t, h, http.MethodPost, "/ingest", segmentJSON(assetID, segments-1-i)); rec.Code != http.StatusAccepted {
					t.Errorf("ingest: got status %d", rec.Code)
				}
			})
		}
		wg.Go(func() {
			for range segments {
				do(t, h, http.MethodGet, "/manifest/"+assetID, "")
			}
		})
	}
	wg.Wait()

	want := make([]int, segments)
	for i := range want {
		want[i] = i
	}
	for a := range assets {
		m := getManifest(t, h, fmt.Sprintf("show/load/ep%d", a))
		if got := indexes(m); !slices.Equal(got, want) {
			t.Errorf("asset %d: got %v, want 0..%d", a, got, segments-1)
		}
	}
}
