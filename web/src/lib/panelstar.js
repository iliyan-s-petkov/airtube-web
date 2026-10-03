// Moves one of the panel's own header buttons (the star by default) into a dock or sheet header and puts it back: moved, never copied.
export function createStarSlot(doc, selector = '.panel-star') {
  let star = null
  let marker = null

  function release() {
    if (star) {
      // A marker that left the document means the panel was torn down: the star is an orphan.
      if (marker?.isConnected) marker.replaceWith(star)
      else star.remove()
    }
    star = null
    marker = null
  }

  // Takes the panel's star into `head`, before `before`; keeps the one it already holds.
  function take(panel, head, before) {
    const own = star && marker && panel?.contains(marker) ? star : null
    const next = panel?.querySelector(selector) ?? own
    if (!next) {
      release()
      return
    }
    if (next !== star) {
      release()
      marker = doc.createComment('star')
      next.before(marker)
      star = next
    }
    if (star.parentNode !== head) head.insertBefore(star, before)
  }

  return { take, release }
}
