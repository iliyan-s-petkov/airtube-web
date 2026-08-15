package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"airbg.org/internal/httpx"
)

// The national fallback view — roughly Bulgaria's centre, at a zoom that fits
// the country — is configuration (airbg.yaml frontend.default_zoom /
// default_lon / default_lat), not a constant here. It is used for a visitor
// abroad, a visitor whose location cannot be determined, and every request in a
// deployment without Cloudflare; the home page's map island opens on the same
// three numbers, so they have exactly one home.

type locateBody struct {
	Slug string  `json:"slug"`
	Name string  `json:"name"`
	Lon  float64 `json:"lon"`
	Lat  float64 `json:"lat"`
	Zoom int     `json:"zoom"`
	// Source is "geoip" or "default", so the frontend can decide whether to
	// show a "showing all of Bulgaria" hint. Without it a default view is
	// indistinguishable from a confident but wrong one.
	Source string `json:"source"`
}

// handleLocate resolves an approximate starting view from Cloudflare's visitor
// location headers.
//
// No browser geolocation prompt, no IP stored, no cookie. The coordinates are
// used for one ST_Covers lookup and discarded — the response carries an area
// slug, never the caller's own position.
func (d Deps) handleLocate(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	body := locateBody{
		Lon:    d.Config.Frontend.DefaultLon,
		Lat:    d.Config.Frontend.DefaultLat,
		Zoom:   d.Config.Frontend.DefaultZoom,
		Source: "default",
	}

	// degraded distinguishes the two ways this handler can return the default
	// body WITHOUT having established anything about the caller: a shed request
	// and a failed lookup. The bytes are identical to the confident default, so
	// nothing downstream could tell them apart — which is exactly why the flag
	// exists, and why it is set at the two branches that know, not guessed at
	// the bottom. It is read once, by cacheControlFor.
	degraded := false

	// The headers are honoured ONLY from a trusted peer, exactly as
	// CF-Connecting-IP is. Anything else is caller-supplied data.
	if httpx.PeerTrustedFrom(r.Context()) {
		if lon, lat, ok := headerCoords(r); ok {
			// A full admission pool DEGRADES this route rather than failing it.
			// The lookup is skipped entirely — so shedding still costs no
			// database work, which is the whole point — and the response is the
			// same national view the error path below returns. Answering 503
			// here would make /locate less available under load than it is when
			// the query outright fails, for a request that needs no successful
			// query at all. The refusal is still counted, inside
			// tryAdmitQuery, so an operator sees the control fire.
			release, admitted := d.tryAdmitQuery("locate")
			if !admitted {
				degraded = true
			} else {
				slug, err := d.Store.AreaAtPoint(r.Context(), lon, lat)
				release()
				if err != nil {
					// A failed lookup degrades to the national view rather than
					// failing the request: the caller wanted a map to open, and
					// a wider map is a worse answer but still an answer.
					degraded = true
					slog.Warn("locate lookup failed", "error", err)
				} else if meta, known := snap.KnownSlugs[slug]; known {
					body = locateBody{
						Slug: meta.Slug, Name: meta.NameBG,
						Lon: meta.CentroidLon, Lat: meta.CentroidLat,
						Zoom: meta.DefaultZoom, Source: "geoip",
					}
				}
			}
		}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControlFor(degraded))
	w.Header().Set("Vary", "CF-IPLatitude, CF-IPLongitude")
	_, _ = w.Write(encoded)
}

// cacheControlFor picks /locate's cache directive from whether the answer is a
// fact or a symptom.
//
// Never "public" in either case: the response varies by caller IP, and a shared
// cache storing it would hand one visitor's city to everyone behind the same
// edge node.
//
// The 300s lifetime is only correct for an answer that will still be true in
// 300s — a resolved area, or a lookup that ran and found the caller outside
// every area. A shed request and a failed query produce the same national body
// while establishing nothing, and caching that for five minutes would pin a
// visitor to the wide map long after a spike that lasted seconds. Shedding is a
// designed, routine event, so this is a case that will be hit rather than a
// theoretical one. no-store rather than max-age=0 because the directive should
// state the intent: there is nothing here worth keeping.
func cacheControlFor(degraded bool) string {
	if degraded {
		return "no-store"
	}
	return "private, max-age=300"
}

// headerCoords parses and range-checks Cloudflare's visitor-location headers.
//
// Range-checking matters because PostGIS does not object to latitude 999: it
// builds a point nothing contains and returns no rows, so a garbage header
// would look identical to a visitor abroad. Validating here keeps the "default"
// source meaningful.
func headerCoords(r *http.Request) (lon, lat float64, ok bool) {
	latStr := r.Header.Get("CF-IPLatitude")
	lonStr := r.Header.Get("CF-IPLongitude")
	if latStr == "" || lonStr == "" {
		return 0, 0, false
	}

	lat, errLat := strconv.ParseFloat(latStr, 64)
	lon, errLon := strconv.ParseFloat(lonStr, 64)
	if errLat != nil || errLon != nil {
		return 0, 0, false
	}
	// NaN and ±Inf parse successfully from "nan"/"inf" and pass a naive range
	// check, because every comparison against NaN is false.
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lon, lat, true
}
