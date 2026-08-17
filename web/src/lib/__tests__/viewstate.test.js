import { describe, it, expect } from 'vitest'
import { parseHash, serialiseHash } from '../viewstate.js'

const opts = { metrics: ['P1', 'P2', 'temperature'], defaultMetric: 'P2' }

describe('parseHash', () => {
  it('reads both keys in either order', () => {
    expect(parseHash('#metric=P1&sensor=1234', opts)).toEqual({ metric: 'P1', sensorId: 1234 })
    expect(parseHash('#sensor=1234&metric=P1', opts)).toEqual({ metric: 'P1', sensorId: 1234 })
  })

  it('falls back to the server default for an unknown metric', () => {
    expect(parseHash('#metric=plutonium', opts).metric).toBe('P2')
  })

  // The two keys are independent: a bad sensor id must not take the metric with
  // it. This is the whole reason validation is per-key rather than a single
  // "is this hash valid" check.
  it('keeps a valid metric when the sensor id is junk', () => {
    expect(parseHash('#metric=temperature&sensor=abc', opts))
      .toEqual({ metric: 'temperature', sensorId: null })
  })

  it('rejects zero, negative and fractional sensor ids', () => {
    for (const bad of ['0', '-5', '1.5']) {
      expect(parseHash(`#sensor=${bad}`, opts).sensorId).toBeNull()
    }
  })

  it('ignores unknown keys so a future #period= cannot break old links', () => {
    expect(parseHash('#period=7d&metric=P1', opts)).toEqual({ metric: 'P1', sensorId: null })
  })

  it('treats an empty hash as the default state', () => {
    expect(parseHash('', opts)).toEqual({ metric: 'P2', sensorId: null })
  })
})

describe('serialiseHash', () => {
  it('is empty for the default state, so shared URLs stay clean', () => {
    expect(serialiseHash({ metric: 'P2', sensorId: null }, 'P2')).toBe('')
  })

  it('omits the metric when it is the default', () => {
    expect(serialiseHash({ metric: 'P2', sensorId: 1234 }, 'P2')).toBe('#sensor=1234')
  })

  it('omits the sensor when none is open', () => {
    expect(serialiseHash({ metric: 'P1', sensorId: null }, 'P2')).toBe('#metric=P1')
  })

  it('round-trips through parseHash', () => {
    const state = { metric: 'temperature', sensorId: 77 }
    expect(parseHash(serialiseHash(state, 'P2'), opts)).toEqual(state)
  })
})
