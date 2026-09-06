import { describe, it, expect, afterEach, vi } from 'vitest'
import {
  STATUSES,
  DEFAULT_STATUS,
  filterByStatus,
  getSensorStatus,
  setSensorStatus,
  onSensorStatusChange,
  resetSensorFilterForTests,
} from '../sensorfilter.svelte.js'

afterEach(() => resetSensorFilterForTests())

const sensor = (value) => ({ properties: { value } })

describe('filterByStatus', () => {
  it('passes every sensor through when nothing is filtered', () => {
    const features = [sensor(3.2), sensor(null)]
    expect(filterByStatus(features, 'all')).toHaveLength(2)
  })

  it('keeps only the sensors reporting the selected metric', () => {
    const got = filterByStatus([sensor(3.2), sensor(null), sensor(7)], 'active')
    expect(got.map((f) => f.properties.value)).toEqual([3.2, 7])
  })

  it('keeps only the silent ones', () => {
    const got = filterByStatus([sensor(3.2), sensor(null)], 'inactive')
    expect(got.map((f) => f.properties.value)).toEqual([null])
  })

  // 0 µg/m³ is a reading, and the cleanest sensor in the area is exactly the
  // one a falsy test would misfile as silent.
  it('counts a zero reading as reporting, not as silence', () => {
    expect(filterByStatus([sensor(0)], 'active')).toHaveLength(1)
    expect(filterByStatus([sensor(0)], 'inactive')).toHaveLength(0)
  })

  // An unknown status must not silently empty the map. Failing open leaves the
  // visitor with more than they asked for; failing closed leaves them with a
  // blank frame that reads as "no sensors here".
  it('draws everything rather than nothing for a status it does not know', () => {
    expect(filterByStatus([sensor(3.2), sensor(null)], 'nonsense')).toHaveLength(2)
  })
})

describe('the shared status', () => {
  // The kit opens on "with data". This site opens on all of them: a silent
  // sensor is already drawn in the no-data colour and named in the legend, so
  // the kit's default would remove visible sensors and overstate coverage.
  //
  // Asserted on DEFAULT_STATUS as well as on the live value: afterEach resets
  // the store, so by the time any `it()` runs, the initial value has already
  // been overwritten with the reset's — and a mutation of the opening literal
  // survived a test that only read getSensorStatus().
  it('opens on all sensors, not on the kit default', () => {
    expect(DEFAULT_STATUS).toBe('all')
    expect(getSensorStatus()).toBe(DEFAULT_STATUS)
  })

  // A FRESH module, not the shared one: every other case in this file runs
  // after an afterEach has already reset the store, so none of them can see the
  // value the browser actually opens on. A mutation of the opening $state
  // survived until this case existed.
  it('starts at the default before any reset has touched it', async () => {
    vi.resetModules()
    const fresh = await import('../sensorfilter.svelte.js')
    expect(fresh.getSensorStatus()).toBe('all')
  })

  it('notifies its subscribers when the status changes', () => {
    const seen = vi.fn()
    onSensorStatusChange(seen)
    setSensorStatus('active')
    expect(getSensorStatus()).toBe('active')
    expect(seen).toHaveBeenCalledWith('active')
  })

  // The map repaints on every notification, so a no-op set that still notified
  // would repaint the map on every click of the radio already selected.
  it('says nothing when the status is set to what it already is', () => {
    const seen = vi.fn()
    onSensorStatusChange(seen)
    setSensorStatus('all')
    expect(seen).not.toHaveBeenCalled()
  })

  // The value reaches filterByStatus and, through it, what the map draws. A
  // value from outside the vocabulary is a bug, not a new filter.
  it('refuses a status outside the vocabulary', () => {
    setSensorStatus('nonsense')
    expect(getSensorStatus()).toBe('all')
    expect(STATUSES).toEqual(['all', 'active', 'inactive'])
  })

  it('stops notifying an unsubscribed listener', () => {
    const seen = vi.fn()
    const off = onSensorStatusChange(seen)
    off()
    setSensorStatus('inactive')
    expect(seen).not.toHaveBeenCalled()
  })
})
