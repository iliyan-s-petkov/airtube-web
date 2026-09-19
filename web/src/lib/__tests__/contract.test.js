// The killing test for this phase: a JS list that disagrees with
// contract.json fails here, whatever quoting a hand-written copy uses. The
// other half — contract.json disagreeing with the Go constants it is generated
// from — is TestContractJSONIsCurrent in internal/snapshot.
import { describe, it, expect } from 'vitest'
import contract from '../contract.json'
import { WINDOWS, LIVE_WINDOW, WINDOW_CHOICES } from '../mapwindow.js'
import { SPANS } from '../timelapse.js'
import { POINT_RESOLUTION_KM } from '../hexes.js'

describe('the JS consumers say exactly what contract.json says', () => {
  // By value AND order: the window selector and the timelapse span picker are
  // positional against the server's own label lists, so a reordered copy is a
  // map offering the wrong labels rather than a build failure.
  it('mapwindow: WINDOWS is contract.windows, in order', () => {
    expect(WINDOWS).toEqual(contract.windows.map((w) => w.name))
  })

  it('mapwindow: LIVE_WINDOW is contract.live_window', () => {
    expect(LIVE_WINDOW).toBe(contract.live_window)
  })

  it('mapwindow: WINDOW_CHOICES is live first, then the published windows', () => {
    expect(WINDOW_CHOICES).toEqual([contract.live_window, ...contract.windows.map((w) => w.name)])
  })

  it('timelapse: SPANS is contract.spans, in order', () => {
    expect(SPANS).toEqual(contract.spans.map((s) => s.span))
  })

  it('hexes: POINT_RESOLUTION_KM is contract.hex.point_resolution_km', () => {
    expect(POINT_RESOLUTION_KM).toBe(contract.hex.point_resolution_km)
  })
})
