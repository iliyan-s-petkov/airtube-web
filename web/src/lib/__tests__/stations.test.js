import { describe, it, expect } from 'vitest'
import { stationsOf, stationMembers, readingAt, measuresAt, metricColumnsOf } from '../stations.js'
import { normaliseSensor } from '../sensors.svelte.js'
import { countSensors } from '../sensorcount.js'
import { sensorFeatures } from '../../islands/map.js'

// One address in Varna as upstream actually publishes it — a particulate box
// and a climate box, same coordinate, disjoint metrics — plus a lone sensor
// down the road.
function body() {
  return {
    sensors: {
      id: [5966, 5965, 19774],
      type: ['BME280', 'SDS011', 'SDS011'],
      lon: [27.976, 27.976, 27.9372],
      lat: [43.224, 43.224, 43.2178],
      quality: ['ok', 'ok', 'ok'],
      station: [5965, 5965, 19774],
      measures: [['humidity', 'pressure', 'temperature'], ['P1', 'P2'], ['P1', 'P2']],
      first_seen: ['2023-11-01T08:00:00Z', '2024-03-05T10:00:00Z', '2025-01-09T12:00:00Z'],
      last_seen: ['2026-09-07T18:10:00Z', '2026-09-07T18:20:00Z', '2026-09-07T17:55:00Z'],
      P1: [null, 12, 5],
      P2: [null, 7, 3],
      temperature: [18, null, null],
      humidity: [60, null, null],
      pressure: [1012, null, null],
      // A column nobody here has a microphone for. It exists because the server
      // emits every canonical metric for every device.
      noise_LAeq: [null, null, null],
    },
  }
}

describe('stationsOf', () => {
  it('groups the devices at one address and leaves a lone device alone', () => {
    const got = stationsOf(body())
    expect(got.map((s) => s.station)).toEqual([5965, 19774])
    expect(got[0].indices).toHaveLength(2)
    expect(got[1].indices).toHaveLength(1)
  })

  // Members ascend by device id whatever order the rows arrived in, which is
  // what makes "the first member that has one" a stable answer rather than an
  // accident of the payload.
  it('orders the members by device id, not by row order', () => {
    const [first] = stationsOf(body())
    const ids = body().sensors.id
    expect(first.indices.map((i) => ids[i])).toEqual([5965, 5966])
  })

  // A payload without the column is every device standing alone — the
  // behaviour from before stations existed, not a crash and not one giant
  // station of undefined.
  it('falls back to one station per device when the column is missing', () => {
    const old = { sensors: { id: [1, 2], lon: [23, 23], lat: [42, 42] } }
    expect(stationsOf(old).map((s) => s.station)).toEqual([1, 2])
  })

  it('has nothing to group in an empty body', () => {
    expect(stationsOf({})).toEqual([])
    expect(stationsOf(null)).toEqual([])
  })
})

describe('stationMembers', () => {
  // The whole point of the join: a deep link naming the climate box and a
  // click on the marker (which carries the particulate box's id) must open
  // the same address.
  it('resolves the same station from either member', () => {
    expect(stationMembers(body(), 5965).station).toBe(5965)
    expect(stationMembers(body(), 5966).station).toBe(5965)
  })

  it('answers null for an id this area does not carry', () => {
    expect(stationMembers(body(), 999)).toBe(null)
    expect(stationMembers(body(), null)).toBe(null)
  })
})

describe('readingAt', () => {
  const b = body()
  const [varna] = stationsOf(b)

  it('takes the reading from whichever device has one, and names that device', () => {
    expect(readingAt(b, varna.indices, 'P1')).toEqual({ value: 12, sensorId: 5965 })
    expect(readingAt(b, varna.indices, 'temperature')).toEqual({ value: 18, sensorId: 5966 })
  })

  // Silence is a reading's absence, not a device's: the station still names a
  // device, so the panel always has something to chart against.
  it('still names a device when no member has a reading', () => {
    const quiet = { sensors: { id: [1, 2], station: [1, 1], P1: [null, null] } }
    const [s] = stationsOf(quiet)
    expect(readingAt(quiet, s.indices, 'P1')).toEqual({ value: null, sensorId: 1 })
  })

  // 0 µg/m³ is the cleanest air the network can report, and a truthiness test
  // would file it as no reading at all.
  it('treats zero as a reading', () => {
    const zero = { sensors: { id: [1], station: [1], P2: [0] } }
    expect(readingAt(zero, [0], 'P2').value).toBe(0)
  })
})

// The bug the whole change exists for: a panel opened on the particulate box
// said "no reading" for temperature, humidity and pressure while the box
// beside it was measuring all three.
describe('normaliseSensor over a station', () => {
  it('fills every metric measured at the address, from whichever device measured it', () => {
    const s = normaliseSensor(body(), 5965)
    expect(s.values).toEqual({ P1: 12, P2: 7, temperature: 18, humidity: 60, pressure: 1012 })
  })

  it('records which device each reading came from, so the chart can ask it', () => {
    const s = normaliseSensor(body(), 5965)
    expect(s.sources.P1).toBe(5965)
    expect(s.sources.temperature).toBe(5966)
  })

  it('opens the same station from either device id', () => {
    expect(normaliseSensor(body(), 5966).id).toBe(5965)
  })

  // A station with one misbehaving device has a problem worth showing; the
  // healthy device beside it must not bury it.
  it('surfaces a member flagged as anything other than ok', () => {
    const b = body()
    // On the SECOND member, so a station flag that only ever read the first
    // one would still say ok.
    b.sensors.quality = ['stuck', 'ok', 'ok']
    expect(normaliseSensor(b, 5965).flag).toBe('stuck')
  })

  // The bug this half of the change exists for: a particulate box claimed
  // temperature, humidity, pressure and both noise metrics, and the panel
  // printed "no reading" for all five under a station that has no such hardware.
  it('claims only what the hardware at the address measures', () => {
    const s = normaliseSensor(body(), 19774)
    expect(s.id).toBe(19774)
    expect(s.values).toEqual({ P1: 5, P2: 3 })
  })

  // The other side of the same coin: a metric the address DOES measure stays,
  // null, so the panel can say the reading is missing rather than pretend the
  // instrument is not there.
  it('keeps a measured metric with no usable reading, as null', () => {
    const b = body()
    b.sensors.pressure = [null, null, null]
    expect(normaliseSensor(b, 5965).values.pressure).toBe(null)
  })
})

describe('metricColumnsOf', () => {
  // Every meta column left in would become a panel row: a station listing
  // "measures" or "station" beside its PM10 reading.
  it('is the metric columns and nothing else', () => {
    expect(metricColumnsOf(body())).toEqual(['P1', 'P2', 'temperature', 'humidity', 'pressure', 'noise_LAeq'])
  })
})

describe('measuresAt', () => {
  it('is the union over the devices at the address', () => {
    const b = body()
    const [varna] = stationsOf(b)
    expect(measuresAt(b, varna.indices)).toEqual(['P1', 'P2', 'temperature', 'humidity', 'pressure'])
  })

  // A response served before the server published the column: every column is
  // fair game, which is exactly how it behaved then.
  it('falls back to every metric column when the body has no measures', () => {
    const b = body()
    delete b.sensors.measures
    expect(measuresAt(b, [0])).toEqual(metricColumnsOf(b))
  })

  it('names no metric for a device that measures nothing', () => {
    const b = body()
    b.sensors.measures = [[], [], []]
    expect(measuresAt(b, [0, 1])).toEqual([])
  })
})

describe('sensorFeatures over stations', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#111111' }, { upper: null, colour: '#222222' }] }]

  // Two dots at one coordinate meant the second was drawn exactly on top of
  // the first and could never be clicked.
  it('draws one dot per address, carrying the station id', () => {
    const features = sensorFeatures(body(), 'P2', scales, '#9ca3af')
    expect(features).toHaveLength(2)
    expect(features.map((f) => f.properties.id)).toEqual([5965, 19774])
  })

  // The climate box has no P2 at all. Painting the address from it would grey
  // out a station that is reporting perfectly well.
  it('colours the address from the device that has the metric', () => {
    const features = sensorFeatures(body(), 'P2', scales, '#9ca3af')
    expect(features[0].properties.value).toBe(7)
    expect(features[0].properties.colour).not.toBe('#9ca3af')
  })
})

describe('countSensors over stations', () => {
  // The count line sits under the map and has to agree with it.
  it('counts addresses, not devices', () => {
    expect(countSensors(body(), 'P2')).toEqual({ total: 2, active: 2, silent: 0 })
  })

  it('counts an address silent only when no device there reports the metric', () => {
    expect(countSensors(body(), 'temperature')).toEqual({ total: 2, active: 1, silent: 1 })
  })
})
