// Mutual exclusion for a group of native <details> menus.
//
// The masthead's language and theme pickers are two independent <details>. The
// element gives each one open/closed on its own, which is correct for a
// disclosure and wrong for a menu bar: both panels opened at once and overlapped.
// This adds the three behaviours a menu is expected to have — one open at a
// time, a click elsewhere closes it, Escape closes it — without replacing the
// native control, so the no-JavaScript language picker keeps working unchanged.
//
// The set is queried on every event rather than captured once: the theme picker
// is server-rendered empty and replaced by its island after this is wired.

export function soleOpen(root, selector = 'details.langpick') {
  if (!root) return

  const menus = () => [...root.querySelectorAll(selector)]
  const closeAll = (except) => {
    for (const d of menus()) if (d !== except) d.open = false
  }

  // Delegated on the root, not on each <details>: a summary click is what opens
  // or closes its own menu, and the browser's default action already does that.
  // All this has to decide is what happens to the others.
  root.addEventListener('click', (event) => {
    const menu = event.target.closest?.(selector)
    if (menu) closeAll(menu)
  })

  document.addEventListener('click', (event) => {
    if (!event.target.closest?.(selector)) closeAll()
  })

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') closeAll()
  })
}
