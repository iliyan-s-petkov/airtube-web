# quality

Scores readings for plausibility. Readings are flagged, never discarded: the
map greys them out and aggregates exclude them (spec §5.2, §6).

Checks run in order and the first failure wins — clamp, range, stuck, spatial.

## The clamp sentinel

A clamp sentinel is the value an instrument reports when it has run off the top
of its own scale. The SDS011 pegs at 1999.9 µg/m³ for PM10 and 999.9 for PM2.5,
and both arrive as ordinary readings with no marker of any kind.

Measured on the 2026-09-04 upstream payload for the six enabled countries: 30
sensors at exactly those values, always as a pair, never one without the other,
all SDS011.

**Why it is not left to the range check.** 999.9 sits inside the configured P2
range of 0–1000, so the sentinel passed, scored `ok`, and entered the reference
population — dragging the neighbourhood median its own neighbours were scored
against. Its P1 twin at 1999.9 fell outside the P1 range and was caught. One
physical event, two verdicts, and a corrupted spatial check for everything
within 15 km of a pegged sensor.

**Why exact equality.** A range would swallow the genuine readings just below
the peg; 999.8 is a real concentration. The sentinel is a fixed constant the
firmware emits verbatim, never a computed value that lands nearby.

**Why it runs before the range check.** Only one of the two PM sentinels would
be caught by range at all, so without an explicit order the pair splits across
two flags. A saturated instrument is also a more specific diagnosis than an
implausible number.

**Why a clamped reading never reaches `hist.Observe`.** A sensor pegged for
hours is stuck by any definition, but `clamped` already says why, and feeding
the sentinel to the history would make the stuck check fire on the recovery
rather than on the fault.

**Why its own flag rather than `out_of_range`.** The two need different
responses: a saturated sensor is working, a broken one is not. A rising
`clamped` count in the cycle log is an air quality event, not a data quality
one. `FlagClamped.Usable()` is false, so it stays out of aggregates.

Config lives in `airbg.yaml` under `quality.clamp_sentinels`, and validation
rejects a sentinel equal to its metric's range ceiling — the flag would then
depend on which check ran first. The database enum value is added by migration
00009.
