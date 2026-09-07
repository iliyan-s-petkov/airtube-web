// What the map has loaded, and how to send it somewhere — published for the
// finder, which is a separate island beside it.
//
// The area list is the map's own payload (see islands/map.js's refresh), so the
// finder on the map tab searches exactly what the map is drawing, with no
// second copy and no request of its own. The camera goes the other way: the map
// registers how it moves, and the finder calls it by name.
//
// A $state because the list arrives after the field mounts: the finder reads it
// through a getter prop, so the options appear the moment the first area-tier
// response lands.
let areas = $state([])
let select = null

export function setMapAreas(next) {
  areas = next ?? []
}

export function getMapAreas() {
  return areas
}

// provideAreaSelect returns its own teardown, the same shape as the map's other
// subscriptions.
export function provideAreaSelect(fn) {
  select = fn
  return () => { if (select === fn) select = null }
}

// False when no map is mounted on this page — the list tab's finder never calls
// this, but a caller must be able to tell "no map" from "went there".
export function selectMapArea(area) {
  return select ? select(area) : false
}
