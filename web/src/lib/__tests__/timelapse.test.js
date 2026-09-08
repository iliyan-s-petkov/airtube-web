// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import {
  SPANS, knownSpan, spanFor, timelapseURL, frameBody, frameCount, frameTime,
  cursor, step, seek, mountPlayer,
} from '../timelapse.js'

const BODY = {
  metric: 'P2',
  resolution_km: 15,
  cells: [[23.0, 42.0], [27.0, 43.0]],
  frames: [
    { t: '2026-09-08T06:00:00Z', v: [10, null] },
    { t: '2026-09-08T07:00:00Z', v: [null, 0] },
  ],
}

describe('the span vocabulary', () => {
  it('is the list the server publishes', () => {
    expect(SPANS).toEqual(['24h', '7d'])
    expect(knownSpan('24h')).toBe(true)
    expect(knownSpan('48h')).toBe(false)
  })

  // A reader looking at a week and pressing play means the week.
  it('follows the map window when the server animates it', () => {
    expect(spanFor('7d')).toBe('7d')
    expect(spanFor('24h')).toBe('24h')
  })

  // Live and 48h have no animation. Falling back to the day rather than sending
  // a span the server refuses: a 400 on the first press of play is not a feature.
  it('falls back to the day for a window with no animation', () => {
    expect(spanFor('')).toBe('24h')
    expect(spanFor('48h')).toBe('24h')
    expect(spanFor('nonsense')).toBe('24h')
  })

  it('never puts an unpublished span on the URL', () => {
    expect(timelapseURL('P2', '48h')).toBe('/api/v1/timelapse?metric=P2&span=24h')
    expect(timelapseURL('P1', '7d')).toBe('/api/v1/timelapse?metric=P1&span=7d')
  })
})

describe('reading a frame', () => {
  // The one place the geometry and the numbers are paired. Get it wrong and
  // every cell is drawn with its neighbour's reading.
  it('pairs each cell with the value at its own index', () => {
    const b = frameBody(BODY, 0)
    expect(b.resolution_km).toBe(15)
    expect(b.hexes).toEqual([
      { lon: 23.0, lat: 42.0, values: { P2: 10 } },
      { lon: 27.0, lat: 43.0, values: { P2: null } },
    ])
  })

  // 0 µg/m³ is a reading and an absent cell is not; the map draws them
  // differently, so this must not collapse the two.
  it('keeps a reported zero apart from an absent one', () => {
    const b = frameBody(BODY, 1)
    expect(b.hexes[0].values.P2).toBeNull()
    expect(b.hexes[1].values.P2).toBe(0)
  })

  // A frame carries no sensor count. A made-up 1 would be a popup claiming the
  // cell holds one device.
  it('states no count', () => {
    expect(frameBody(BODY, 0).hexes[0].n).toBeUndefined()
  })

  it('is empty rather than broken past the last frame', () => {
    expect(frameBody(BODY, 9).hexes).toEqual([])
    expect(frameBody(null, 0).hexes).toEqual([])
    expect(frameCount(null)).toBe(0)
    expect(frameCount(BODY)).toBe(2)
  })

  it('reads a frame time, and refuses an unusable one', () => {
    expect(frameTime(BODY, 0).toISOString()).toBe('2026-09-08T06:00:00.000Z')
    expect(frameTime({ frames: [{ t: 'not a date' }] }, 0)).toBeNull()
    expect(frameTime(BODY, 9)).toBeNull()
  })
})

describe('the playhead', () => {
  it('wraps at the end, because an animation is watched round', () => {
    const c = cursor(2)
    expect(step(c)).toBe(1)
    expect(step(c)).toBe(0)
  })

  it('clamps a scrub to the frames that exist', () => {
    const c = cursor(3)
    expect(seek(c, 5)).toBe(2)
    expect(seek(c, -1)).toBe(0)
    expect(seek(c, 1.7)).toBe(1)
  })

  it('stays put with nothing loaded', () => {
    const c = cursor(0)
    expect(step(c)).toBe(0)
    expect(seek(c, 3)).toBe(0)
  })
})

describe('mountPlayer', () => {
  const labels = { label: 'Time', playLabel: 'Play', pauseLabel: 'Pause' }

  function mount() {
    const frame = document.createElement('div')
    frame.id = 'map'
    const host = document.createElement('div')
    document.body.append(frame, host)
    return { ui: mountPlayer(frame, { ...labels, host }), host }
  }

  // Icon-only, so the name has to be somewhere a screen reader reaches.
  it('names the button without a word in it', () => {
    const { ui } = mount()
    expect(ui.button.textContent.trim()).toBe('')
    expect(ui.button.getAttribute('aria-label')).toBe('Play')
    expect(ui.button.getAttribute('title')).toBe('Play')
    expect(ui.button.querySelector('svg').getAttribute('aria-hidden')).toBe('true')
  })

  // The button IS the state: a reader who has started an animation must be able
  // to see that pressing again stops it.
  it('becomes Pause while playing, and Play again after', () => {
    const { ui } = mount()
    const d = () => ui.button.querySelector('path').getAttribute('d')
    const playGlyph = d()

    ui.playing(true)
    expect(ui.button.getAttribute('aria-pressed')).toBe('true')
    expect(ui.button.getAttribute('aria-label')).toBe('Pause')
    expect(d()).not.toBe(playGlyph)

    ui.playing(false)
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
    expect(ui.button.getAttribute('aria-label')).toBe('Play')
    expect(d()).toBe(playGlyph)
  })

  // A slider over a map that is not animating is a control with nothing behind
  // it, and dragging it would be a promise the page cannot keep.
  it('hides the scrubber and the clock until an animation is loaded', () => {
    const { ui } = mount()
    expect(ui.slider.hidden).toBe(true)
    expect(ui.clock.hidden).toBe(true)

    ui.show(24)
    expect(ui.slider.hidden).toBe(false)
    expect(ui.clock.hidden).toBe(false)
    expect(ui.slider.max).toBe('23')

    ui.show(0)
    expect(ui.slider.hidden).toBe(true)
  })

  it('reports a press and a scrub', () => {
    const { ui } = mount()
    const pressed = []
    const scrubbed = []
    ui.ontoggle(() => pressed.push(true))
    ui.onscrub((i) => scrubbed.push(i))

    ui.button.click()
    ui.show(10)
    ui.slider.value = '4'
    ui.slider.dispatchEvent(new Event('input'))

    expect(pressed).toHaveLength(1)
    expect(scrubbed).toEqual([4])
  })

  it('puts the playhead and its time on screen', () => {
    const { ui } = mount()
    ui.show(24)
    ui.at(7, '13:00')
    expect(ui.slider.value).toBe('7')
    expect(ui.clock.textContent).toBe('13:00')
  })

  it('is appended into the host', () => {
    const { ui, host } = mount()
    expect(ui.root.parentElement).toBe(host)
  })

  // style-src has no 'unsafe-inline', so anything positioned from here would be
  // dropped by the browser and the control would land wherever the flow put it.
  it('writes no inline style', () => {
    const { ui } = mount()
    for (const el of [ui.root, ui.button, ui.slider, ui.clock]) {
      expect(el.getAttribute('style')).toBeNull()
    }
  })
})
