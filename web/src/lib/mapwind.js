// The map-driving seam between the map and the wind island (./islands/wind.js
// holds the pure builders: WIND_SOURCE_ID, windFeatures, arrowPaint and
// friends). Kept separate so the island stays importable on its own.
import { getJSON } from './api.js'
import {
  WIND_LAYER_ID, WIND_SOURCE_ID, windFeatures, windField, windIsStale, windLabel,
} from '../islands/wind.js'
import { paintSource } from './mapdata.js'

// setWind is the whole wind control: fetch once, then show or hide.
//
// It takes the state asked for rather than flipping the one it finds, because
// the control is now a checkbox in the layers menu: a toggle would drift out of
// step with the box the moment a fetch failed. It RETURNS the state actually
// reached, which is what lets the box correct itself.
//
// Exported and given an injectable fetch for the same reason initData is —
// the "no jsdom" rule puts a real MapLibre instance out of reach, so the
// behaviour that matters (the disclosure appears with the arrows and never
// without them) is only testable through a fake map and a fake chrome.
//
// A failed fetch leaves the layer off rather than raising the map's error
// banner: /api/v1/wind answers 503 whenever no forecast covers the current
// hour, which is an ordinary state for an optional overlay, and an error
// banner is reserved for the data the page actually exists to show.
export async function setWind(map, cfg, chrome, state, on, fetchJSON = getJSON) {
  if (!on) {
    state.on = false
    map.setLayoutProperty(WIND_LAYER_ID, 'visibility', 'none')
    chrome.showWind(false, '')
    return false
  }
  // A second request while the first fetch is in flight is dropped, not queued:
  // getJSON already dedupes the request, but two resolutions would each flip
  // the layer and the later one could turn on a layer the visitor just asked
  // to turn off.
  if (state.loading) return state.on
  if (!state.body) {
    state.loading = true
    try {
      state.body = await fetchJSON('/api/v1/wind')
    } catch {
      chrome.showWind(false, '')
      return false
    } finally {
      state.loading = false
    }
  }
  paintWind(map, state)
  map.setLayoutProperty(WIND_LAYER_ID, 'visibility', 'visible')
  state.on = true
  chrome.showWind(true, windLabel(state.body, cfg.t))
  return true
}

// refreshWind is the wind layer's share of a refresh: nothing, until the hour
// the held forecast is valid for has passed.
//
// Everything else the button reloads moves on the five-minute ingest cycle. The
// forecast does not (see windIsStale), so this drops the body only on an hour
// boundary — and refetches there and then only if the layer is on. With it off,
// the cleared body is enough: the next toggle fetches the current hour.
export async function refreshWind(map, cfg, chrome, state, now = new Date(), fetchJSON = getJSON) {
  if (!windIsStale(state.body, now)) return false
  state.body = null
  if (state.on) await setWind(map, cfg, chrome, state, true, fetchJSON)
  return true
}

// paintWind redraws the arrows for the viewport the map is currently showing.
//
// The served field is one national lattice at the snapshot's hex resolution, so
// drawing it as-is means the arrows thin out as the reader zooms in and are
// gone entirely over a single neighbourhood — a layer that empties itself looks
// exactly like a forecast that failed. windField resamples the same vectors
// onto a screen-sized lattice instead; the values are still the model's, only
// repeated, and the disclosure already names the grid they came from.
export function paintWind(map, state) {
  if (!state.body) return
  if (!map.getSource(WIND_SOURCE_ID)) return
  const b = map.getBounds?.()
  const features = b
    ? windField(state.body, {
      bounds: [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()],
      zoom: map.getZoom(),
    })
    : windFeatures(state.body)
  paintSource(map, WIND_SOURCE_ID, features)
}
