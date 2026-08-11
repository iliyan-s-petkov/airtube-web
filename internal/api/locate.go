package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"airbg.org/internal/httpx"
)

// The national fallback view: roughly Bulgaria's centre, at a zoom that fits the
// country. Used for a visitor abroad, a visitor whose location cannot be
// determined, and every request in a deployment without Cloudflare.
const (
	defaultLon  = 25.4858
	defaultLat  = 42.7339
	defaultZoom = 7
)

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
		Lon: defaultLon, Lat: defaultLat, Zoom: defaultZoom, Source: "default",
	}

	// The headers are honoured ONLY from a trusted peer, exactly as
	// CF-Connecting-IP is. Anything else is caller-supplied data.
	if httpx.PeerTrustedFrom(r.Context()) {
		if lon, lat, ok := headerCoords(r); ok {
			release, admitted := d.admitQuery(w, "locate")
			if !admitted {
				return
			}
			slug, err := d.Store.AreaAtPoint(r.Context(), lon, lat)
			release()
			if err != nil {
				// A failed lookup degrades to the national view rather than
				// failing the request: the caller wanted a map to open, and a
				// wider map is a worse answer but still an answer.
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

	encoded, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// private: the response varies by caller IP. A shared cache storing it
	// would hand one visitor's city to everyone behind the same edge node.
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Vary", "CF-IPLatitude, CF-IPLongitude")
	_, _ = w.Write(encoded)
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
