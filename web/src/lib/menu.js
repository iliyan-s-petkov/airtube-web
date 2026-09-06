// The two rules every pop-up panel on this site obeys, in one place: a menu
// closes when the reader clicks past it, and it closes on Escape. The column
// menu and the metric menu both need them, and two copies of a dismissal rule
// is how one of them ends up staying open behind the reader's next click.
//
// Plain functions over a DOM node rather than a Svelte action: they are also
// what the components' $effects want to return as cleanup, and a function that
// returns its own teardown says the whole rule in one place.

// mousedown, not click: click arrives after the pointer has already decided
// where it is going, and a menu that is still open at that point has to be
// dismissed twice.
export function closeOnOutside(el, close, target = window) {
  const away = (e) => {
    if (el && !el.contains(e.target)) close()
  }
  target.addEventListener('mousedown', away)
  return () => target.removeEventListener('mousedown', away)
}

// Escape is bound to the window, not to the panel: the panel is open OVER the
// page, and the reader's focus may be anywhere by the time they want it gone.
// preventDefault only when a menu was actually open, so Escape keeps its other
// meanings — clearing a search box, closing a dialog — the rest of the time.
export function closeOnEscape(isOpen, close) {
  return (e) => {
    if (e.key !== 'Escape' || !isOpen()) return
    e.preventDefault()
    close()
  }
}
