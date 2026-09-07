import { describe, it, expect } from 'vitest'
import { panelRows, detailRows } from '../sensorview.js'

const options = [
  { metric: 'P2', label: 'PM2.5' },
  { metric: 'P1', label: 'PM10' },
  { metric: 'temperature', label: 'Temperature' },
]
const scales = [{ metric: 'P2', unit: 'µg/m³', bands: [] }, { metric: 'P1', unit: 'µg/m³', bands: [] }]

describe('panelRows', () => {
  it('lists every metric the sensor reports, in the switcher order', () => {
    const rows = panelRows({ values: { P1: 30, P2: 12 } }, options, scales)
    expect(rows.map((r) => r.metric)).toEqual(['P2', 'P1'])
  })

  // A metric the sensor does not measure is omitted; a metric it measures but
  // has no CURRENT value for is kept and marked missing. Collapsing the two
  // would tell a reader a working sensor does not measure PM10.
  it('keeps a reported metric with no current value and marks it missing', () => {
    const rows = panelRows({ values: { P1: null, P2: 12 } }, options, scales)
    expect(rows.find((r) => r.metric === 'P1')).toMatchObject({ missing: true, value: null })
  })

  it('carries the unit from the scales response', () => {
    expect(panelRows({ values: { P2: 12 } }, options, scales)[0].unit).toBe('µg/m³')
  })

  it('leaves the unit empty for a metric with no scale table', () => {
    const rows = panelRows({ values: { temperature: 21 } }, options, scales)
    expect(rows[0].unit).toBe('')
  })

  it('returns nothing for a sensor with no values at all', () => {
    expect(panelRows({ values: {} }, options, scales)).toEqual([])
  })
})

const LABELS = {
  devices: 'Devices',
  hardware: 'Hardware',
  since: 'In our data since',
  updated: 'Last reading',
  coords: 'Coordinates',
}

const station = {
  lat: 42.6977123,
  lon: 23.3218456,
  devices: [
    { id: 42, type: 'SDS011', firstSeen: '2024-03-05T10:00:00Z', lastSeen: '2026-09-07T18:20:00Z' },
    { id: 43, type: 'DHT22', firstSeen: '2023-11-01T08:00:00Z', lastSeen: '2026-09-07T18:10:00Z' },
  ],
}

describe('detailRows', () => {
  it('lists every device standing at the address', () => {
    const rows = detailRows(station, LABELS, 'en-GB')
    expect(rows.find((r) => r.key === 'devices')).toMatchObject({
      label: LABELS.devices, value: '42, 43',
    })
  })

  it('names each distinct hardware type once', () => {
    const two = detailRows(station, LABELS, 'en-GB').find((r) => r.key === 'hardware')
    expect(two.value).toBe('SDS011, DHT22')

    const same = { ...station, devices: station.devices.map((d) => ({ ...d, type: 'SDS011' })) }
    expect(detailRows(same, LABELS, 'en-GB').find((r) => r.key === 'hardware').value).toBe('SDS011')
  })

  // The station has been in our data since its OLDEST box arrived and is as
  // fresh as its NEWEST reading — not since, or as of, whichever device the
  // payload happened to list first.
  it('spans the station lifetime across its devices', () => {
    const rows = detailRows(station, LABELS, 'en-GB')
    expect(rows.find((r) => r.key === 'since').value).toContain('2023')
    expect(rows.find((r) => r.key === 'updated').value).toContain('07/09/2026')
  })

  it('prints the position at four decimals, latitude first', () => {
    expect(detailRows(station, LABELS, 'en-GB').find((r) => r.key === 'coords').value)
      .toBe('42.6977, 23.3218')
  })

  // Zero is a real coordinate off the Gulf of Guinea, not a missing one. It is
  // outside Bulgaria, but the guard must test finiteness, not truthiness.
  it('keeps a zero coordinate', () => {
    const rows = detailRows({ ...station, lat: 0, lon: 0 }, LABELS, 'en-GB')
    expect(rows.find((r) => r.key === 'coords').value).toBe('0.0000, 0.0000')
  })

  it('omits a row it has nothing to say about rather than printing it empty', () => {
    const rows = detailRows(
      { devices: [{ id: 42, type: '', firstSeen: null, lastSeen: null }] },
      LABELS, 'en-GB',
    )
    expect(rows.map((r) => r.key)).toEqual(['devices'])
  })

  it('returns nothing for a sensor it was handed nothing about', () => {
    expect(detailRows(null, LABELS, 'en-GB')).toEqual([])
  })
})
