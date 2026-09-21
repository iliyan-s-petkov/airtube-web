import { describe, it, expect } from 'vitest'
import { gaugeModel, gaugeRange, arcPath } from '../gauge.js'
import { rampColour } from '../ramp.js'

const p2Bands = [
  { upper: 5, colour: '#0a0' },
  { upper: 15, colour: '#8a0' },
  { upper: 50, colour: '#fa0' },
  { upper: 90, colour: '#f80' },
  { upper: 140, colour: '#f40' },
  { upper: null, colour: '#a00' },
]
const p1Bands = [
  { upper: 15, colour: '#0a0' },
  { upper: 45, colour: '#8a0' },
  { upper: 120, colour: '#fa0' },
  { upper: 195, colour: '#f80' },
  { upper: 270, colour: '#f40' },
  { upper: null, colour: '#a00' },
]
const temperatureBands = [
  { upper: -10, colour: '#00a' },
  { upper: 0, colour: '#0aa' },
  { upper: 10, colour: '#0a0' },
  { upper: 20, colour: '#fa0' },
  { upper: 30, colour: '#f80' },
  { upper: null, colour: '#a00' },
]
const humidityBands = [
  { upper: 30, colour: '#f80' },
  { upper: 40, colour: '#fa0' },
  { upper: 60, colour: '#0a0' },
  { upper: 80, colour: '#fa0' },
  { upper: null, colour: '#f80' },
]
const pressureBands = [
  { upper: 990, colour: '#00a' },
  { upper: 1005, colour: '#0aa' },
  { upper: 1020, colour: '#0a0' },
  { upper: 1035, colour: '#fa0' },
  { upper: null, colour: '#f80' },
]
const coBands = [
  { upper: 4000, colour: '#0a0' },
  { upper: null, colour: '#a00' },
]

const scales = [
  { metric: 'P2', unit: 'µg/m³', ceiling: 500, bands: p2Bands },
  { metric: 'P1', unit: 'µg/m³', ceiling: 500, bands: p1Bands },
  { metric: 'temperature', unit: '°C', ceiling: 45, bands: temperatureBands },
  { metric: 'humidity', unit: '%', ceiling: 100, bands: humidityBands },
  { metric: 'pressure', unit: 'hPa', ceiling: 1050, bands: pressureBands },
  { metric: 'CO', unit: 'µg/m³', ceiling: 10000, bands: coBands },
]

describe('gaugeRange', () => {
  it('computes the six served ranges', () => {
    expect(gaugeRange('P2', p2Bands, 500)).toEqual({ min: 0, max: 190 })
    expect(gaugeRange('P1', p1Bands, 500)).toEqual({ min: 0, max: 345 })
    expect(gaugeRange('temperature', temperatureBands, 45)).toEqual({ min: -20, max: 40 })
    expect(gaugeRange('humidity', humidityBands, 100)).toEqual({ min: 0, max: 100 })
    expect(gaugeRange('pressure', pressureBands, 1050)).toEqual({ min: 950, max: 1050 })
    expect(gaugeRange('CO', coBands, 10000)).toEqual({ min: 0, max: 8000 })
  })
})

describe('gaugeModel', () => {
  it('clamps a reading above the range to 1', () => {
    expect(gaugeModel({ metric: 'humidity', value: 250, missing: false }, scales).fraction).toBe(1)
  })

  it('clamps a reading below the range to 0', () => {
    expect(gaugeModel({ metric: 'temperature', value: -40, missing: false }, scales).fraction).toBe(0)
  })

  it('reports no fraction or colour for a missing row', () => {
    const model = gaugeModel({ metric: 'humidity', value: null, missing: true }, scales)
    expect(model.fraction).toBeNull()
    expect(model.colour).toBeNull()
  })

  it('scales a banded metric against its own arc range, coloured by the ramp', () => {
    const model = gaugeModel({ metric: 'P2', value: 20, missing: false }, scales)
    expect(model.fraction).toBe(20 / 190)
    expect(model.colour).toBe(rampColour(20, p2Bands))
    expect(model.stops.length).toBe(p2Bands.length)
    const fractions = model.stops.map((s) => s.fraction)
    expect(fractions).toEqual([...fractions].sort((a, b) => a - b))
    expect(fractions[fractions.length - 1]).toBe(1)
  })

  it('places a pressure reading at its fraction of the pressure arc', () => {
    const model = gaugeModel({ metric: 'pressure', value: 1010.22, missing: false }, scales)
    expect(model.fraction).toBeCloseTo(0.6022, 4)
  })

  it('places a temperature reading at its fraction of the temperature arc', () => {
    const model = gaugeModel({ metric: 'temperature', value: 21.03, missing: false }, scales)
    expect(model.fraction).toBeCloseTo(0.6838, 4)
  })

  it('puts the first temperature band stop at 1/6 of the arc', () => {
    const model = gaugeModel({ metric: 'temperature', value: -15, missing: false }, scales)
    expect(model.stops[0].fraction).toBeCloseTo(1 / 6, 4)
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

  it('always uses a large-arc flag of 0', () => {
    expect(arcPath(0, 0.9)).toContain(' 0 0 1 ')
  })
})
