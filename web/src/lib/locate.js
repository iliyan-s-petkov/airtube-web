// The response's `source` field is the whole decision. "default" is the
// server saying "I could not place you, here is the country" — it is NOT a
// location, and treating it as one both jars the view and adopts a slug the
// visitor never asked for, which is what unlocks the per-area sensor tier.
export function applyLocate(body, { defaultView }) {
  const stay = { move: false, centre: [defaultView.lon, defaultView.lat], zoom: defaultView.zoom, slug: null }
  if (!body || body.source !== 'geoip') return stay
  return { move: true, centre: [body.lon, body.lat], zoom: body.zoom, slug: body.slug ?? null }
}
