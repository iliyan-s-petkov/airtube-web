// The "more below" strip under the map. Scrolls instead of navigating: a
// #below-map hash would be parsed as view state and drop #sensor=/#metric=.
export function scrollCue(root = document, win = window) {
  root.addEventListener('click', (event) => {
    const cue = event.target.closest?.('a.scroll-cue')
    if (!cue) return
    const target = root.querySelector('#below-map')
    if (!target) return
    event.preventDefault()
    const reduce = win.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    target.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' })
  })
}
