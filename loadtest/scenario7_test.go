//go:build load

package loadtest

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sas/api"
	"sas/store"
)

const (
	writers           = 200
	readers           = 50
	assets            = 500
	segmentsPerWriter = 1000
	segmentsPerAsset  = writers * segmentsPerWriter / assets
)

func TestScenario7(t *testing.T) {
	s := store.New()
	srv := httptest.NewServer(api.New(s, slog.New(slog.DiscardHandler)).Routes())
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: writers + readers}}

	writeLatency := make([][]time.Duration, writers)
	readLatency := make([][]time.Duration, readers)
	var notFound atomic.Int64
	done := make(chan struct{})

	var readersWG sync.WaitGroup
	for r := range readers {
		readersWG.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				url := fmt.Sprintf("%s/manifest/show/load/ep%d", srv.URL, rand.IntN(assets))
				status, d, err := timed(client, http.MethodGet, url, "")
				if err != nil {
					t.Error(err)
					return
				}
				if status == http.StatusNotFound {
					notFound.Add(1)
				}
				readLatency[r] = append(readLatency[r], d)
			}
		})
	}

	start := time.Now()
	var writersWG sync.WaitGroup
	for w := range writers {
		writersWG.Go(func() {
			for i := range segmentsPerWriter {
				n := w*segmentsPerWriter + i
				body := fmt.Sprintf(
					`{"asset_id":"show/load/ep%d","segment_index":%d,"cdn_url":"https://cdn.example.com/%d.ts","received_at":"2024-05-01T10:00:00Z"}`,
					n%assets, n/assets, n,
				)
				status, d, err := timed(client, http.MethodPost, srv.URL+"/ingest", body)
				if err != nil {
					t.Error(err)
					return
				}
				if status != http.StatusAccepted {
					t.Errorf("ingest: got status %d", status)
					return
				}
				writeLatency[w] = append(writeLatency[w], d)
			}
		})
	}
	writersWG.Wait()
	elapsed := time.Since(start)
	close(done)
	readersWG.Wait()

	for a := range assets {
		segments, _, ok := s.Get(fmt.Sprintf("show/load/ep%d", a))
		if !ok || len(segments) != segmentsPerAsset {
			t.Errorf("asset %d: got %d segments, want %d", a, len(segments), segmentsPerAsset)
			continue
		}
		if !slices.IsSortedFunc(segments, func(x, y store.Segment) int { return cmp.Compare(x.Index, y.Index) }) {
			t.Errorf("asset %d: segments not sorted", a)
		}
	}

	writes, reads := slices.Concat(writeLatency...), slices.Concat(readLatency...)
	t.Logf("elapsed %v: %.0f writes/s, %.0f reads/s, %d reads were 404",
		elapsed.Round(time.Millisecond), float64(len(writes))/elapsed.Seconds(), float64(len(reads))/elapsed.Seconds(), notFound.Load())
	t.Logf("writes: %s", summarise(writes))
	t.Logf("reads:  %s", summarise(reads))
}

func timed(client *http.Client, method, url, body string) (int, time.Duration, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return 0, 0, fmt.Errorf("build request: %w", err)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, 0, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, time.Since(start), nil
}

func summarise(latencies []time.Duration) string {
	slices.Sort(latencies)
	at := func(q float64) time.Duration {
		return latencies[int(q*float64(len(latencies)-1))].Round(10 * time.Microsecond)
	}
	return fmt.Sprintf("n=%d p50=%v p99=%v max=%v", len(latencies), at(0.50), at(0.99), latencies[len(latencies)-1].Round(10*time.Microsecond))
}
