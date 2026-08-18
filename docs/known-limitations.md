# Known limitations

What the map does not tell you on the page. Everything here is a property of
the data, not a bug to be fixed by a code change, and each one can change how a
number should be read.

## City areas are not all the same kind of thing

**13 of the 27 cities are city-proper polygons. The other 14 are whole
municipalities.**

OpenStreetMap has an `admin_level=8` city-proper boundary for only 13 of
Bulgaria's 27 oblast capitals. The other 14 have no settlement-level boundary in
OSM at all, so their `admin_level=5` municipality boundary is used instead.

| Geometry | Cities |
|---|---|
| **City proper** (13) | Sofia, Ruse, Dobrich, Razgrad, Targovishte, Silistra, Shumen, Varna, Gabrovo, Veliko Tarnovo, Kyustendil, Burgas, Vidin |
| **Whole municipality** (14) | Blagoevgrad, Vratsa, Kardzhali, Lovech, Montana, Pazardzhik, Pernik, Pleven, Plovdiv, Sliven, Smolyan, Haskovo, Stara Zagora, Yambol |

**Why it matters.** A municipality covers considerably more ground than its
city. A sensor several kilometres outside the built-up area — in a village, on a
hillside, beside a main road through farmland — is attributed to the city and
moves its average. Rural air is usually cleaner in summer and, where solid fuel
heating is common, dirtier in winter, so the bias is not in a fixed direction.

**Reading the map with this in mind.** Comparing Plovdiv (municipality) with
Varna (city proper) compares two different kinds of area. The comparison is
still informative; it is not like-for-like. Within one city, over time, the
geometry is constant and the trend is unaffected.

This is an acceptable approximation for point-in-polygon aggregation and not a
survey-grade city outline. If OSM later gains an `admin_level=8` boundary for
one of the 14, adopting it will change that city's polygon and therefore its
history — see `docs/boundary-regeneration.md`.

## There are 27 cities, not 28

Bulgaria has 28 oblasti but only 27 distinct oblast capitals: Sofia is the
administrative seat of both Sofia-grad and Sofia Oblast. Committing a 28th city
would mean the same physical place holding two rows under two slugs.

## A sensor near a border may be attributed to the neighbouring area

The OSM boundaries are simplified with a tolerance of roughly 200 m, which cuts
vertex counts by 20–25× at no meaningful cost to point-in-polygon accuracy —
except within about 200 m of a border, where a sensor can fall on the other
side of the simplified line from the real one.

Affected sensors are few and the effect on an area average is proportionally
smaller still, but a single sensor's *area label* near an oblast or district
edge should not be treated as authoritative.

## The national boundary is coarser than the area boundaries

`bulgaria.geojson` comes from Natural Earth at 1:10 m scale, while the oblast,
city and district boundaries come from OSM. The national outline is used for one
purpose only — rejecting sensors outside Bulgaria before they are stored — so
its coarseness affects only sensors within roughly a kilometre of an
international border, where inclusion or exclusion may not match the legal
frontier.

## Coverage is uneven and reported, not hidden

Sensor density varies enormously: Sofia has many, some oblasti have very few. An
area's aggregate is only as representative as the sensors inside it, and an area
with two sensors is not measuring the same thing as an area with two hundred.
Coverage state is served alongside every aggregate rather than being folded
invisibly into the value — check it before drawing a conclusion from a
sparse area.

## The sensors are not reference instruments

The readings come from the sensor.community network: low-cost optical
particulate sensors, sited and maintained by volunteers. They are good at
showing relative change over time and gross spatial patterns, and they are not
regulatory monitors. High humidity in particular inflates optical PM readings.
Absolute values should not be compared against a legal limit as though they came
from a reference station.

---

Sources, licences and the exact provenance of every boundary file:
`data/boundaries/README.md`. How to rebuild them:
`docs/boundary-regeneration.md`.
