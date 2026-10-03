// From 1024px the home page shows the open sensor only in the map's panel; app.css hides the section under the map.
export const DOCKED_HOST = 'place-host--docked'
const WIDE = '(min-width: 1024px)'

export function sectionHidden(win, el) {
  return !!el?.closest?.(`.${DOCKED_HOST}`) && !!win?.matchMedia?.(WIDE)?.matches
}
