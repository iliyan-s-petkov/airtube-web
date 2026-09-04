# The wind overlay

An optional map layer showing forecast wind on the same hex grid the PM
aggregate uses. It is the only layer in this application that is **not a
measurement**, and most of the decisions below exist to keep that distinction
visible rather than convenient.

## Why a forecast at all

sensor.community has no anemometers. Every sensor we ingest reports some subset
of PM, temperature, humidity, pressure and noise; none of them reports wind. So
the choice was between a met model, a derivation, or nothing.

Deriving geostrophic wind from our own pressure field was considered and
rejected. In this terrain a pressure gradient across two sensors is mostly an
*altitude* gradient — Sofia sits in a basin at 550 m ringed by mountains over
2000 m — so the derived vector would be an artefact of topography, and it would
arrive on the map wearing the same clothes as our measured data. A wrong number
that looks locally sourced is worse than a right number that is openly external.

## Provider

Open-Meteo, model `ecmwf_ifs025`, over plain JSON:

```
GET https://api.open-meteo.com/v1/forecast
    ?latitude=<a,b,c>&longitude=<x,y,z>
    &hourly=wind_speed_10m,wind_direction_10m
    &models=ecmwf_ifs025&wind_speed_unit=ms&timezone=UTC
```

No API key, no new Go module — a `net/http` GET and `encoding/json`, the same
shape as `internal/upstream`. ECMWF's own Open Data service was the alternative
and serves GRIB2, which cannot be decoded without either a dependency or a lot
of hand-rolled binary parsing; the standing rule is no new third-party
dependency.

### Two properties of the response that the code depends on

**Results come back in request order, and their coordinates are not yours.**
The response is a JSON array, one object per requested point, in the order
requested. Each object's `latitude`/`longitude` are the *model grid cell* the
point fell into, not what was asked for: request 42.7/23.3 and the answer says
42.75/23.25. Matching responses to hexes by coordinate therefore does not work
— several hexes legitimately share one cell, and none of them matches exactly.
The client keys results by **request index** and asserts the array length
matches the request. `TestResponseIsKeyedByIndexNotCoordinate` pins this.

**The model is coarser than our grid.** `ecmwf_ifs025` is a 0.25° grid, roughly
25 km at this latitude, against our 15 km hexes. Neighbouring hexes will often
carry identical vectors because they are reading the same cell. This is
upsampling and the overlay says so: the on-map label names the model and its
resolution, so a user who sees a block of identical arrows can tell that is the
model's grid rather than a suspiciously uniform wind.

## Storage

Wind is stored, not proxied. A cache-through proxy would have meant an upstream
outage rendering a blank layer and no history at all; storing it makes the
overlay behave like every other datum in the application — the API reads our
own database, and a fetch failure degrades to stale data with an honest
timestamp rather than to nothing.

Migration `00010` adds a `wind_forecast` hypertable keyed by `(hex_q, hex_r,
valid_at)`. It is keyed by **axial hex coordinate, not by centre lon/lat**: the
centre is a computed float that moves if `HexResolutionKM` or `hexRefLat` ever
changes, whereas the axial pair is the grid's own identity. When the resolution
does change the stored rows become meaningless, which is why the migration
records the resolution they were written at.

Retention is short. A forecast that has been superseded is not history worth
keeping — we keep enough to serve the current overlay and to see what the model
said versus what the PM did, and no more.

## Which points get queried

The hexes in the current snapshot, and only those. The grid is not a fixed
tiling of the region: hexes exist where sensors exist, so the query set is
around 300 points and changes slowly. Querying a fixed national grid would mean
fetching wind for empty countryside that no overlay will ever draw.

Points are batched per request rather than sent as one enormous URL.

## Rendering

One arrow per hex, rotated to the wind direction, scaled by speed. Not an
animated particle field: that is a canvas render loop over the whole map with a
real cost on mobile battery, it needs a static fallback for
`prefers-reduced-motion` anyway — so the arrows get built either way — and the
animation's persuasiveness is itself a problem for a layer whose whole risk is
looking more authoritative than it is.

Direction follows the meteorological convention Open-Meteo uses: the direction
the wind is coming **from**. The arrow is drawn pointing the way the air is
going, which is the opposite, and this is exactly the sort of thing that is
wrong in production for a year, so it is pinned by a test rather than by a
comment.

The arrow is the character U+2192 in a MapLibre symbol layer, not a sprite
image: the marker labels already render text through the style's own glyph
source, so a glyph needs neither `addImage` nor an SDF sprite to tint, and it
is one fewer build artefact to ship and keep in sync. The glyph points east, so
the layer rotates it by `bearing - 90`; the feature property stays a compass
bearing, because that is what every other surface in this codebase calls it.

The layer is off by default and toggled by a button in the map's bottom-right
corner. It is fetched once, on first switch-on, and cached for the page's
lifetime — the payload is a single forecast hour for the whole country, so it
does not change while the visitor pans, and it is deliberately not tied to the
viewport, the tier, or the selected metric. A 503 (no forecast covers the
current hour) leaves the layer off and raises nothing: that is an ordinary
state for an optional overlay, and the map's error banner belongs to the data
the page exists to show.

## Labelling

Whenever the layer is on, a persistent, translated line names the model, its
grid resolution, and the forecast's valid time. Not behind an info icon, not
only in the legend: the point of choosing a met API over a derivation was to
avoid presenting inference as measurement, and a disclosure the user has to go
looking for does not achieve that.

## Configuration

Under `wind:` in `airbg.yaml`. The layer is off unless configured — an operator
running this application without a met provider gets a site with no wind
overlay, not a site with a broken one.
