# Streaming Availability Service

A Go HTTP service that records which video segments are ready to serve and
returns an ordered manifest per asset. Built with the Go standard library only.

## Running locally

Requires Go 1.27.1 or later, as pinned in `go.mod`.

```sh
go run .
```

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | Listen port. Render sets this automatically. |
| `DRAIN_TIMEOUT` | `20s` | How long shutdown waits for in-flight requests. Any `time.ParseDuration` value, e.g. `5s`, `1m`. Invalid or non-positive values stop startup with an error. |

```sh
curl -X POST localhost:8080/ingest \
  -d '{"asset_id":"show/thunderbirds/season/1/ep3","segment_index":0,"cdn_url":"https://cdn.example.com/seg0.ts","received_at":"2024-05-01T10:00:00Z"}'

curl localhost:8080/manifest/show/thunderbirds/season/1/ep3
curl localhost:8080/health
```

## Running the tests

```sh
go test ./...
go test -race ./...
```

Or use the `Makefile`:

| Target | Runs |
|---|---|
| `make test` | Unit and handler tests (also the default target) |
| `make race` | The same tests with the race detector |
| `make bench` | Benchmarks only, with allocation stats |
| `make cover` | Tests with per-package coverage |
| `make check` | `test` then `race`, the two commands the brief requires |
| `make load` | The Scenario 7 load test (see below) |

The tests use the standard `testing` and `net/http/httptest` packages only, so
there is no assertion library. API tests go through `Routes()`, so they cover
the real routing table and path parsing, not just the handler functions.

## Layout

| Path | Responsibility |
|---|---|
| `main.go` | Configuration, wiring, server lifecycle and graceful shutdown |
| `api/` | HTTP handlers, routing, request validation, JSON responses |
| `store/` | Concurrent in-memory segment store |
| `loadtest/` | Scenario 7 load test, behind the `load` build tag |

## Design decision: per-asset locking

The store uses two levels of locking instead of one global mutex. A
`sync.Map` maps each `asset_id` to its own asset record, and each record has
its own `sync.RWMutex` guarding its segment list. I chose `sync.Map` for the
top level because the Go documentation describes it as optimised for keys
that are written once and read many times, and for goroutines working on
disjoint sets of keys, which matches this workload: an asset is created on its
first segment and only looked up after that. Writes to different assets
therefore never contend, and many CDN pollers can read the same asset at once
under the read lock. Segments are kept sorted on write using a binary search,
so a duplicate index is found and overwritten in place, and an out-of-order
segment is inserted at its correct position. I sort on write rather than on
read because the CDN polls far more often than the transcoder writes, so each
segment is placed once instead of the whole list being sorted on every poll.
`Get` returns a copy taken under the read lock, so the handler can encode the
response after the lock is released without racing a concurrent write. The
alternative I considered was a fixed number of shards, each a map plus a
mutex, keyed by a hash of `asset_id`; it bounds lock count and memory, but the
shard count needs tuning and unrelated assets in the same shard block each
other.

## Assumptions and behaviour

- **State is in memory only** and is lost on restart, as the brief allows.
Running more than one instance would give each its own inconsistent view.
Every deploy and restart starts empty, and Render's free tier spins a
service down after 15 minutes without traffic, which also resets state.
- **All four fields are required.** The brief shows only `cdn_url` missing,
so I read "missing required fields" as all of them. An explicit `null`
counts as missing.
- `segment_index` **is decoded into a pointer** because `0` is a valid index
and a plain `int` cannot tell `0` apart from an absent field.
`received_at` uses `IsZero()` instead, because the zero time is never a real
timestamp.
- **A duplicate segment updates in place** (last write wins on `cdn_url`).
The manifest never holds two entries for one index. A late resend carrying
an older URL will overwrite a newer one; ordering writes by `received_at`
would fix that if it ever matters.
- `last_updated` **is the latest** `received_at` **seen for the asset**, and never
moves backwards when old segments arrive late. It is the transcoder's
clock, not this service's.
- **Ingest returns** `202 Accepted` as the brief specifies. Strictly, `202`
means the request was received but processing is not complete (RFC 9110),
whereas here the write is synchronous and visible to the very next manifest
read. I kept `202` because it is the specified API contract, and because it
leaves room to make ingest asynchronous later (for example,
queueing writes under burst load) without changing what clients see. If the
contract were mine to set, I would return `201 Created` for a new segment
and `200 OK` for a duplicate, so the transcoder can tell the two apart.
- **All decode failures return the same message** the brief specifies
(invalid JSON, wrong types, an empty body, a bad timestamp, a body over the
limit). The specific cause is logged at `WARN` rather than returned.
- **Asset IDs contain slashes**, so the route is `GET /manifest/{asset_id...}`,
which captures the rest of the path. `/manifest/` with an empty ID returns
`404 asset not found`. Ingest rejects empty path components and `.` or `..` components, since the mux redirects those paths before the manifest handler runs. Other characters must be URL-escaped by clients.
- **Wrong methods return** `405` with an `Allow` header, from the mux itself.
- `GET /` **returns a small JSON index** so the bare deployed URL shows the
service is up. The route is `GET /{$}`, which matches only `/`, because a
catch-all `/` would turn wrong-method `405`s into `404`s.

## Known limitations

- **Light validation.** A negative `segment_index`, a `cdn_url` that is not a
URL, and unknown fields are accepted. Trailing data after the JSON object is rejected.
- **Bodies are capped at 1 MB** and an oversized body returns `400` with the
standard decode message, not `413`.
- `Content-Type` **is not enforced** on ingest.
- **Unknown paths return the mux's plain-text** `404 page not found`, not a
JSON error body.
- **No eviction.** Assets are never removed, so memory grows with the number
of assets and segments ingested.
- **Health is liveness only.** `/health` reports `ok` while the process
serves; there is no separate readiness signal. Once draining starts the
listener is closed, so health checks fail fast rather than reporting `ok`.
- **Logging covers rejections and lifecycle only**, as structured JSON. There
is no per-request access log, request ID or metrics yet.
- The JSON tags live on `store.Segment`, so the storage type doubles as the
wire format. A separate response type would decouple them, at the cost of a
copy per request.

## Graceful shutdown

`signal.NotifyContext` cancels a context on `SIGTERM` or `SIGINT`. The server
runs in a goroutine; `main` waits for either that context or a server error.
On a signal it calls `http.Server.Shutdown` with a `DRAIN_TIMEOUT` deadline:
the listener closes immediately, in-flight requests run to completion, and
the process exits 0. If the deadline passes first, it exits 1. After the first
signal the handler is released, so a second `SIGTERM` or Ctrl-C kills the
process immediately. `DRAIN_TIMEOUT` should stay below the platform's grace
period before it sends `SIGKILL`: Render's default is 30s (configurable up to
300s), so the 20s default fits. The server's `WriteTimeout` (10s) limits response writing; it does not cancel handler execution. The shutdown deadline bounds how long the process waits for draining.

Verified manually by starting a slow upload, sending `SIGTERM` mid-request,
and confirming the request still returned `202` while new connections were
refused.

## Load scenario: 200 writers, 500 assets, 50 readers

`loadtest/` runs this scenario through a real HTTP server: 200 goroutines each
post 1000 segments spread across 500 assets (400 segments per asset) while 50
goroutines poll `/manifest` for random asset IDs. Afterwards it checks that
every asset holds exactly 400 sorted segments. It sits behind a `load` build
tag so `go test -race ./...` stays fast.

```sh
make load
go test -tags load -count=1 -run Scenario7 -cpuprofile cpu.out -mutexprofile mutex.out ./loadtest/
```

Results on an Apple M2 Pro, with the load generator on the same machine:

```text
~23,000 writes/s, ~5,700 reads/s
writes: p50 8.8 ms, p99 14.7 ms
reads:  p50 8.8 ms, p99 14.8 ms
```

No requests failed and no segments were lost or misordered. About a fifth of
reads were `404`, because readers poll assets that have not been created yet.

**Observed bottleneck in the original local run.** The following profile observations include both the client and server in one process and are not an isolated server capacity measurement:

- **About 70% of CPU is socket reads and writes** in the HTTP stack, for the
client and server combined. Per-request HTTP overhead is the limit.
- **About 8% is the manifest handler**, nearly all of it JSON encoding of the
segment list.
- **The store and the ingest handler each take under 0.5% of CPU**, and GC
about 1%.
- **The mutex profile shows no contention on the store's locks.** The
contended locks are inside `net/http`'s connection handling.

So the CPU goes to HTTP handling and JSON encoding rather than the store's
locks, and I expect CPU to be what runs out first. That shows up as rising
latency rather than errors, and sooner on a small host: Render's free tier gets 0.1 CPU and
512 MB RAM, so expect a small fraction of these numbers there.

The costs that grow from here:

- **Manifest size.** Each poll copies and encodes the whole segment list, so
the read cost grows with segments per asset. At 400 segments it is minor,
but a long-form asset with thousands of segments polled by many CDN nodes
turns encoding into the hot path.
- **Memory.** Nothing is evicted, so memory grows with every asset ingested.
- **Overload behaviour.** The server has no concurrency limit, so past
capacity, goroutines pile up and latency climbs instead of excess requests
being rejected quickly.

Writes to one asset are serialised by design, and an out-of-order insert
shifts the slice (O(n)). The duplicate-update benchmark measures two indexes per asset across 500 assets; its roughly 100 ns result does not represent growing manifests. `BenchmarkPutOutOfOrder` separately measures middle insertion into 100-, 1000-, and 10000-segment manifests with setup excluded. `Get` copies 1000 segments in roughly 3 us locally. These microbenchmarks are workload-specific.

**What I would change.**

- Add a content-based or mutation-version `ETag` so CDN pollers get `304 Not Modified`
and skip the body. This cuts the bytes written per poll, which is where
the time goes. The current maximum event timestamp is unsuitable as a change validator: an older event can change a URL without advancing it.
- Cache the encoded manifest bytes per asset, rebuilt on write, so a poll is
a byte copy instead of a fresh JSON encode.
- Add a concurrency limit or load shedding (`503` with `Retry-After`) so
overload fails fast.
- Scale horizontally by partitioning on `asset_id`, or move state to a shared
store, since in-memory state does not survive more than one instance.
- Minor: call `Load` before `LoadOrStore` in `Put`. `LoadOrStore` allocates a
new `asset` on every call even when one exists, which is one of the two
allocations per `Put`.

**Observability before production.**

- Per-route request rate, error rate and latency histograms.
- Ingest-to-availability lag (`now - received_at`), the metric that matters
to playback.
- Asset count and segments per asset, to watch memory growth.
- Go runtime metrics (heap, GC pauses, goroutines) and `net/http/pprof` on an
internal port.
- A request ID carried into the structured logs, and tracing across the
transcoder, this service and the CDN.

## Dependencies

None outside the Go standard library.

## Deployment

Deployed on Render as a Web Service: build
`go build -tags netgo -ldflags '-s -w' -o app`, start `./app`, health check
path `/health`. Render sets `PORT` (default `10000`), so the local `8080`
default never applies there.

Deployed URL: [https://streaming-availability-service.onrender.com](https://streaming-availability-service.onrender.com)
