# airtube-web2.0 — codebase analysis

Scope: entire repo (`www-root/`, ~1150 LOC excluding vendored JS). Single commit, no tests, no CI, no dependency manifest.

## 1. What it is

"Dusty Map": a Leaflet web map of particulate-matter sensors from the luftdaten.info network, with per-sensor 24h charts. Classic LAMP-era PHP — no framework, no build step, no autoloader beyond Composer's for the MongoDB driver.

Data model — **geohash is the join key** between two stores:

| Store | Role | Written by | Read by |
|---|---|---|---|
| MongoDB `dusty.location` | spatial index: one doc per geohash, `location: [lat, long]` | `collectors/api_luftdaten.php` (upsert) | `location.php` via `$geoWithin/$box` |
| InfluxDB `dusty`, measurement `feinstaub` | time series, tagged `geohash`/`sensor_id`/`city`/`street` | same collector (line protocol) | `location.php`, `charts_values.php` |

Flow: browser geolocation → `location.php?lat&long&bounds` → Mongo bbox → geohash list → one InfluxQL query → JSON with `current/average/history` per metric + marker color → Leaflet circles. Marker click → `charts_values.php?geohash` → 10-min means over 24h → ZingChart.

`P1` = PM10, `P2` = PM2.5 (luftdaten naming).

## 2. Security

### S1 — Leaked Google Maps API key (critical)
`lib/geo2addr.class.php:39` hardcodes `AIzaSyBj97hu927Xtgd2IOzX26SCJsvEwb0T_As` in the Geocoding URL, and it is committed to git history. Anyone with repo access can bill against it.
**Action:** rotate the key in Google Cloud Console, move the new one to an env var / untracked config, and apply an API restriction (Geocoding API only, IP-restricted to the collector host). Rotation is required — removing the line does not un-leak a key already in history.

### S2 — InfluxQL injection via `geohash` (high)
`charts_values.php:13,25` interpolate `$_GET['geohash']` into a single-quoted InfluxQL string with no escaping. A `'` in the parameter breaks out of the literal, allowing arbitrary clause injection (read other measurements, expensive scans → DoS). `location.php` is safer by accident: `bounds` passes through `explode` + `floatval`, and the geohashes come from Mongo.
**Action:** whitelist the parameter with `preg_match('/^[0-9a-z]{1,12}$/', $geohash)` before it reaches the query.

### S3 — Stored XSS in the sensor info panel (medium)
`js/main_index.js` `set_status()` and `show_info()` concatenate `point.data.street`, `.city`, `.country` into HTML strings passed to `.html()`. Those strings originate from the luftdaten API and Google Geocoding, i.e. outside your trust boundary, and are stored in InfluxDB tags. A crafted upstream value executes in every visitor's browser.
**Action:** build the DOM with `.text()` / `textContent`, or escape server-side.

### S4 — No egress or error hygiene
`lib/influxdb.class.php` `query()` throws the raw curl response (including the full InfluxDB error body) as an exception message; with `display_errors` on, that reaches the browser. No curl timeouts are set anywhere, so a hung InfluxDB hangs a PHP worker indefinitely.

## 3. Correctness bugs

| # | Location | Problem |
|---|---|---|
| B1 | `collectors/api_luftdaten.php:18-21` | `include('mongo.class.php')` names a file that does not exist (it is `lib/mongolib.class.php`), and all four includes are CWD-relative. The collector fatals on any invocation from outside `www-root/lib/`. **The ingest path is broken as committed.** |
| B2 | `lib/influxdb.class.php:56-64` | `tags_to_string()` escapes only spaces. Line protocol also requires escaping `,` and `=` in tag values. Street names containing a comma silently corrupt the point (extra tag) or are rejected. |
| B3 | `lib/influxdb.class.php:66-73` | `values_to_string()` never quotes field values. The luftdaten payload includes `signal: "-78 dBm"`, which produces invalid line protocol and rejects the whole batch — `push()` sends all lines in one request, so one bad field can drop an entire poll cycle. |
| B4 | `location.php:29` | If the Influx query returns no `series` (geohashes exist in Mongo but no readings in the last day), `$result['body']->results[0]->series` is undefined → warning, then `foreach` over null, then `usort($points)` on an undefined variable. |
| B5 | `js/main_index.js:192-204` | `get_dir()` returns `'down'` when `current > average` and `'up'` when lower — the trend arrow is inverted. `dir` is also an implicit global (no `var`). |
| B6 | `js/main_index.js:59-64` | The 6-second `setInterval` calls `watch_position()` with the *current* position plus zero, so `diff < 50` always short-circuits. The periodic refresh does nothing; `get_points()` on that path is commented out. Data only refreshes on map move. |
| B7 | `js/main_index.js:120-145` | `plotlayers` is never pruned. Markers accumulate across pans and stale sensors are never removed from the map. |
| B8 | `lib/geo2addr.class.php:8,57` | Constructor `file_get_contents` on a missing `data/addr_cache.txt` warns (and `data/` is not in the repo). `parse_addr()` dereferences `$addresses[0]` without checking — `get_addr_from_google()` returns `[]` on a non-OK status, so any geocoding failure warns and returns a half-built object. |
| B9 | `lib/geo2addr.class.php:16-18` | The cache is only flushed in `finish()`, at the very end of the run. A crash mid-poll discards every geocode fetched (and billed) that cycle. |
| B10 | `charts_values.php:9` | `$client_long = $_GET['geohash']` — copy/paste leftover, unused. |
| B11 | `index.php:23` (option), `js/main_index.js:15` | `fullscreenControl` is passed to `L.map()` but the Leaflet fullscreen plugin is never loaded; the option is inert. Same for `font-awesome-4.7.0/`, referenced but absent from the repo. |

## 4. Performance and scale

- **P1 — regex-of-N-geohashes.** `location.php:23` builds `geohash =~ /h1|h2|…/` from every geohash in the viewport, unbounded. At city zoom this is thousands of alternates in one InfluxQL regex — the query string alone can exceed URL limits (the client sends it via GET), and regex tag matching defeats Influx's tag index. Use `geohash =~ /^(h1|h2)$/` at minimum, better: batch by chunks, or switch to `group by geohash` over a bbox-derived geohash *prefix*.
- **P2 — no Mongo index.** `$geoWithin/$box` on `location` works without an index but falls back to a collection scan. A `2d` index on `location` is required for the legacy `$box` shape.
- **P3 — misleading "average".** `location.php` takes `limit 12` per series over a 1-day window, so `average` is the mean of the last 12 samples, not a 24h mean. The UI arrow (B5) compares against this.
- **P4 — cache busting on every asset.** `index.php` appends `?<?php echo time();?>` to CSS and JS, so nothing is ever cached, including on repeat visits.
- **P5 — 684 KB of vendored ZingChart** shipped uncompressed and unversioned.

## 5. Design and licensing notes

- **Coordinate order is `[lat, long]` throughout** — Mongo docs, the `$box` corners, and the Leaflet calls all agree, but this is the inverse of GeoJSON's `[long, lat]`. Any future move to a `2dsphere` index or GeoJSON storage needs a data migration; do not "fix" one side in isolation.
- **Duplicated state.** The collector writes the full tag+value set into both Influx and the Mongo doc. Mongo only needs geohash + location; the duplication is a second source of truth that nothing reconciles.
- **Geohash precision** defaults to `0.00001` (~1 m) in `Lvht\GeoHash::encode`, producing long, effectively unique hashes. That is fine as a sensor ID but makes prefix-based spatial grouping (geohash's main advantage) unusable.
- **ZingChart license.** The bundled header states the code "may not be copied, replicated, or used in any other software or application without prior permission." Confirm the license covers redistribution in this repo, especially if it is or becomes public.
- **No tests, no CI, no `composer.json`** despite a hard runtime dependency on `mongodb/mongodb` via `vendor/autoload.php`.

## 6. Suggested order of work

1. Rotate and restrict the Google API key (S1) — the only item that is actively costing you.
2. Validate `geohash` input (S2); escape sensor strings before `.html()` (S3).
3. Fix the collector includes (B1) and line-protocol escaping/quoting (B2, B3) — until then ingest does not run.
4. Add `composer.json`, create `data/`, add a `2d` index on `dusty.location.location` (P2).
5. Guard the empty-series path (B4), fix the trend arrow (B5), prune stale markers (B7).
6. Bound the geohash regex query (P1).
