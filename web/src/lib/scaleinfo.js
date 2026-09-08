// What the key cannot say in the space it has: which authority drew these
// bands, what each one means, and where to read the guideline itself.
//
// The key on the map is a colour column with numbers on the seams. It answers
// "is this dot bad" and nothing else — a reader who has never met a µg/m³ has
// no way to learn from it that 25 is the EU's daily limit for PM10 and that the
// WHO's is lower. That is the whole reason for the (i) dialog, and it is why
// this file is pure: the band arithmetic below is the part worth testing, and a
// DOM is not needed to test it.

// The scale the map is painting from — matched on `metric`, the same rule
// bandsFor applies in the island, so the dialog can never describe a table the
// map is not using.
export function scaleFor(scales, metric) {
  if (!Array.isArray(scales)) return null
  return scales.find((s) => s.metric === metric) ?? null
}

// A band carries only its own inclusive upper bound; the range a reader wants
// is between that bound and the one below it. The lower edge is therefore the
// PREVIOUS band's upper bound, which is why this takes the whole table and an
// index rather than a band alone.
//
// The first band starts at the bottom of the scale, and for the metrics with a
// negative floor — a temperature — the bottom is not 0. `below` says what to
// print there: with no lower bound stated the range opens downwards.
export function bandRange(bands, i) {
  const upper = bands[i]?.upper
  const lower = i === 0 ? null : (bands[i - 1]?.upper ?? null)
  if (upper == null && lower == null) return ''
  if (upper == null) return `> ${lower}`
  if (lower == null) return `< ${upper}`
  return `${lower}–${upper}`
}

// The dialog's contents, in the reader's language: one row per band, plus the
// notes the scale ships and the link to the guideline behind it.
//
// The unit is stated once, on the heading, and not on every row: eight rows
// each repeating µg/m³ is a column of noise, and the rows are all one metric by
// construction.
//
// A scale with no source — the meteo tables, which are an axis and cite nobody
// — gets no link rather than a dead one. The caller renders what is present.
export function scaleInfo(scale, lang) {
  if (!scale) return null
  const bands = scale.bands ?? []
  return {
    name: scale.name ?? '',
    unit: scale.unit ?? '',
    notes: (lang === 'bg' ? scale.notes_bg : scale.notes) || '',
    source: scale.source || '',
    rows: bands.map((band, i) => ({
      colour: band.colour,
      label: (lang === 'bg' ? band.label_bg : band.label) || '',
      range: bandRange(bands, i),
    })),
  }
}
