import { describe, it, expect } from 'vitest'
import { gaugeModel, arcPath } from '../gauge.js'
import { rampColour } from '../ramp.js'

const bands = [
  { upper: 10, colour: '#0a0' },
  { upper: 25, colour: '#fa0' },
  { upper: null, colour: '#a00' },
]
const scales = [{ metric: 'P2', unit: 'µg/m³', ceiling: 50, bands }]

describe('gaugeModel', () => {
  it('places a mid-range humidity reading at its fraction of the fixed range', () => {
    expect(gaugeModel({ metric: 'humidity', value: 0, missing: false }, []).fraction).toBe(0)
    expect(gaugeModel({ metric: 'humidity', value: 50, missing: false }, []).fraction).toBe(0.5)
    expect(gaugeModel({ metric: 'humidity', value: 100, missing: false }, []).fraction).toBe(1)
  })

  it('clamps a reading above the fixed range to 1', () => {
    expect(gaugeModel({ metric: 'humidity', value: 120, missing: false }, []).fraction).toBe(1)
  })

  it('clamps a reading below the fixed range to 0', () => {
    expect(gaugeModel({ metric: 'temperature', value: -40, missing: false }, []).fraction).toBe(0)
  })

  it('reports no fraction or colour for a missing row', () => {
    const model = gaugeModel({ metric: 'humidity', value: null, missing: true }, [])
    expect(model.fraction).toBeNull()
    expect(model.colour).toBeNull()
  })

  it('scales a banded metric against the served ceiling, coloured by the ramp', () => {
    const model = gaugeModel({ metric: 'P2', value: 20, missing: false }, scales)
    expect(model.fraction).toBe(20 / 50)
    expect(model.colour).toBe(rampColour(20, bands))
    expect(model.stops.length).toBe(bands.length)
    const fractions = model.stops.map((s) => s.fraction)
    expect(fractions).toEqual([...fractions].sort((a, b) => a - b))
    expect(fractions[fractions.length - 1]).toBe(1)
  })

  it('gives no range to an unscaled metric this page does not know', () => {
    const model = gaugeModel({ metric: 'noise', value: 5, missing: false }, [])
    expect(model.range).toBeNull()
    expect(model.fraction).toBeNull()
  })
})

describe('arcPath', () => {
  it('runs the full semicircle from the left end to the right end', () => {
    const d = arcPath(0, 1)
    expect(d).toMatch(/^M\s*10[,\s]50/)
    expect(d).toMatch(/90[,\s]50\s*$/)
  })

  it('stops at the top of the arc at the midpoint', () => {
    const d = arcPath(0, 0.5)
    expect(d).toMatch(/50[,\s]10\s*$/)
  })
})
