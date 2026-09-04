// Package api serves the JSON endpoints from Phase 1 §7.
//
// Every response except /locate comes from the in-memory snapshot, so a request
// costs a pointer load and a byte-slice write. That is the whole
// denial-of-service posture: there is no per-request query to overwhelm.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"airbg.org/internal/admit"
	"airbg.org/internal/config"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// DataSource is the whole database surface this package uses. Narrowed to three
// methods so the handlers can be tested against a stub instead of a container —
// and so it is obvious at a glance which endpoints touch the database at all
// (only /locate and the two series endpoints; everything else is served from
// the snapshot).
type DataSource interface {
	AreaAtPoint(ctx context.Context, lon, lat float64) (string, error)
	SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool, bucket time.Duration) ([]store.Point, error)
	AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool, bucket time.Duration) ([]store.Point, error)
}

type Deps struct {
	Snapshots *snapshot.Holder
	Breadth   *ratelimit.Breadth
	Store     DataSource

	// Config is the resolved configuration. It carries the cache lifetimes, the
	// series period vocabulary and the Retry-After hints — the values that used
	// to be package constants and are now operator tunables.
	Config config.Config

	// SeriesLimiter is a second, much tighter token bucket scoped to the two
	// DB-backed series routes. The global bucket (10 rps) is sized for a page
	// load fanning out to several snapshot reads; a series request costs a
	// PostgreSQL query, and repeating one is free as far as the breadth counter
	// is concerned. See seriesRate.
	//
	// NewRouter substitutes a default when this is nil, so a handler is never
	// unlimited; production passes one explicitly so its evictor is wired to the
	// server's lifetime.
	SeriesLimiter *ratelimit.Limiter

	// Admission bounds how many requests may be inside a database query at
	// once, across all clients. SeriesLimiter bounds one client; this bounds the
	// crowd. NewRouter substitutes a default when nil, so a handler is never
	// admitted without a cap.
	Admission *admit.Semaphore
}

// Cache visibility. This is a security control, not a performance knob.
//
// The anti-extraction design is tiering, not authentication: no endpoint takes a
// bounding box or an unbounded list, so bulk extraction requires ENUMERATING
// areas and sensors, and that is what ratelimit.Breadth counts — distinct slugs
// and sensor IDs per client key. The counter only sees requests that reach the
// origin.
//
//   - cachePublic is for the non-enumerable aggregate responses (/overview,
//     /areas, /meta, /scales). Every visitor asks for the same single resource,
//     there is no per-entity key to walk, and edge caching them is real
//     denial-of-service protection we must not give up.
//   - cachePrivate is for everything keyed by a slug or a sensor ID
//     (/area/{slug}/sensors and both /series endpoints). Marked public, a shared
//     or edge cache would serve a warmed slug without ObserveArea ever seeing
//     the request — so a scraper's distinct-slug count would not grow, and a
//     client that has ALREADY tripped the breadth limit could still read every
//     warm slug straight out of the edge. private keeps the response cacheable
//     in the requesting client's own browser (which is where the repeat traffic
//     of a normal reader lives) while guaranteeing that a request for a
//     DIFFERENT entity always reaches the origin and is counted.
const (
	cachePublic  = "public"
	cachePrivate = "private"
)

func setCacheControl(h http.Header, visibility string, maxAge int) {
	h.Set("Cache-Control", visibility+", max-age="+strconv.Itoa(maxAge))
}

func NewRouter(d Deps) *http.ServeMux {
	// Fail closed: a nil SeriesLimiter would leave the heaviest endpoints bounded
	// only by the global bucket, which is the hole this exists to close.
	//
	// The substitute is the shared, swept defaultSeriesLimiter rather than a fresh
	// per-call one. A fresh one would have no evictor — nothing here has a context
	// to tie one to — so its map would grow for as long as the router lived,
	// making the fail-closed default a quiet leak.
	if d.SeriesLimiter == nil {
		d.SeriesLimiter = defaultSeriesLimiter(d.Config)
	}

	// Fail closed, exactly as with SeriesLimiter: a nil semaphore would leave
	// the database paths uncapped, which is the hole this closes. The default is
	// sized to the API pool's default (config.defaultDBAPIConns) doubled, so a
	// router built without explicit configuration behaves like the deployed one.
	if d.Admission == nil {
		d.Admission = defaultAdmission()
	}

	mux := http.NewServeMux()

	// Method-qualified patterns, so ServeMux answers 405 for anything else
	// without a per-handler check.
	mux.HandleFunc("GET /api/v1/overview", d.handleOverview)
	mux.HandleFunc("GET /api/v1/hexes", d.handleHexes)
	mux.HandleFunc("GET /api/v1/wind", d.handleWind)
	mux.HandleFunc("GET /api/v1/areas", d.handleAreas)
	mux.HandleFunc("GET /api/v1/meta", d.handleMeta)
	mux.HandleFunc("GET /api/v1/scales", d.handleScales)
	mux.HandleFunc("GET /api/v1/area/{slug}/sensors", d.handleAreaSensors)
	mux.HandleFunc("GET /api/v1/area/{slug}/series", d.handleAreaSeries)
	mux.HandleFunc("GET /api/v1/sensor/{id}/series", d.handleSensorSeries)
	mux.HandleFunc("GET /api/v1/locate", d.handleLocate)

	// Phase 1 §7.4's partner API is deferred to Phase 4. The path is reserved
	// now so the version namespace cannot be taken by anything else, and it
	// answers a truthful 501 rather than a 404 that would suggest the design
	// never existed.
	mux.HandleFunc("/api/partner/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotImplemented, "not_implemented",
			"The partner API is not available yet.")
	})

	return mux
}

// errorBody is the single failure envelope. Fixed code, fixed sentence — never a
// Go error string, which would leak table names, file paths and driver
// internals to anyone who can provoke a failure.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Never let an error response be cached: a transient 503 cached for even a
	// few minutes turns a blip into an outage for everyone behind that cache.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: code, Message: message})
}

func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "30")
	writeError(w, http.StatusServiceUnavailable, "unavailable",
		"Data is not ready yet. Please try again shortly.")
}

// serveBody writes one prepared snapshot body, handling revalidation and
// content coding.
// The visibility argument is explicit at every call site rather than defaulted,
// so adding an endpoint forces a decision about whether its response is
// enumerable — see the cachePublic/cachePrivate comment above.
func serveBody(w http.ResponseWriter, r *http.Request, b snapshot.Body, visibility string, maxAge int) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("ETag", b.ETag)
	setCacheControl(h, visibility, maxAge)
	// Vary is mandatory once the body varies by Accept-Encoding: without it a
	// shared cache may hand a gzip body to a client that never asked for one.
	h.Set("Vary", "Accept-Encoding")

	// Compare against every tag the client offers, and honour "*". Substring
	// matching would be wrong in the other direction too — a stale tag that
	// happens to contain the current one would produce a spurious 304.
	if matchesETag(r.Header.Get("If-None-Match"), b.ETag) {
		// 304 must carry no body; the headers above are the whole response.
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if acceptsGzip(r.Header.Get("Accept-Encoding")) && len(b.Gzip) > 0 {
		h.Set("Content-Encoding", "gzip")
		h.Set("Content-Length", strconv.Itoa(len(b.Gzip)))
		_, _ = w.Write(b.Gzip)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(b.JSON)))
	_, _ = w.Write(b.JSON)
}

func matchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		// A weak validator (W/"…") compares equal to the strong tag for the
		// weak comparison that If-None-Match uses.
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

// acceptsGzip parses Accept-Encoding just far enough to honour an explicit
// q=0 refusal. "gzip;q=0" means "do not send me gzip", and a naive
// strings.Contains check reads it as consent.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}
		for _, param := range fields[1:] {
			param = strings.ReplaceAll(strings.ToLower(param), " ", "")
			if strings.HasPrefix(param, "q=") {
				if q, err := strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}
