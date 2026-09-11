package snapshot

import (
	"context"
	"fmt"
	"time"

	"airbg.org/internal/store"
)

// A reader looking at the map is asking one of two questions. What is the air
// like right now — which is what every payload above answers — or what has it
// been like lately, which one bad hour or one dropped sensor cannot swing.
//
// The second question is answered by rebuilding the same payloads from windowed
// means instead of latest readings, and by nothing else: same tiers, same
// coverage rule, same wire shape. That is what lets the client switch windows by
// changing a query parameter rather than by learning a second data model, and
// what lets the server answer from memory either way.
type WindowSpec struct {
	// Name is the wire value of ?window= and the storage key the client keeps
	// its choice under. Short and durational rather than "day"/"week", because
	// it has to survive being read back out of a URL someone shared.
	Name string
	Dur  time.Duration
}

// WindowSpecs are the alternates to the live view, coarsest last. A closed
// list, not an arbitrary duration a caller supplies: each one costs a set of queries and a
// set of encoded bodies per ingest cycle, so the cache stays bounded by a
// constant here rather than by how many distinct windows the internet asks for.
var WindowSpecs = []WindowSpec{
	{Name: "24h", Dur: 24 * time.Hour},
	{Name: "48h", Dur: 48 * time.Hour},
	{Name: "7d", Dur: 7 * 24 * time.Hour},
}

// LiveWindow is the name of the default view: the latest reading from each
// sensor, no averaging. Empty, so a request that names no window gets it, and so
// the canonical URL of the default view has no parameter in it.
const LiveWindow = ""

// KnownWindow reports whether name may be served. The handlers validate against
// it and answer 400 otherwise, rather than silently falling back to live — a
// reader who asked for a week and was quietly shown the last five minutes has no
// way to notice.
func KnownWindow(name string) bool {
	if name == LiveWindow {
		return true
	}
	for _, w := range WindowSpecs {
		if w.Name == name {
			return true
		}
	}
	return false
}

// Window returns the snapshot as seen through one averaging window: the same
// value with the window-varying bodies substituted, so every existing method and
// every existing handler works on it unchanged.
//
// Sharing rather than copying is the point. Boundaries, wind, the known-slug set
// and the area series are properties of the country and of this cycle, not of
// the reader's window, and duplicating them per window would be three more
// copies of the same bytes that could then drift.
//
// An unknown name — or the live one — returns the receiver. Validation belongs
// to the handler, which can answer 400; a lookup that answered nil would turn a
// bad parameter into a 503 at the far end of the call.
func (s *Snapshot) Window(name string) *Snapshot {
	if name == LiveWindow || s == nil {
		return s
	}
	if w, ok := s.Windows[name]; ok {
		return w
	}
	return s
}

// buildWindows produces the alternate views. Its errors are the build's errors:
// unlike the wind and the boundaries, a window that failed to build would be a
// selector option that silently answers with live data.
func buildWindows(ctx context.Context, s *store.Store, snap *Snapshot, now time.Time) error {
	snap.Windows = make(map[string]*Snapshot, len(WindowSpecs))
	for _, spec := range WindowSpecs {
		w, err := buildWindow(ctx, s, snap, now, spec)
		if err != nil {
			return err
		}
		snap.Windows[spec.Name] = w
	}
	return nil
}

func buildWindow(ctx context.Context, s *store.Store, live *Snapshot, now time.Time, spec WindowSpec) (*Snapshot, error) {
	since := now.Add(-spec.Dur)

	countryAggs, err := s.WindowedAreaAggregates(ctx, countryKinds, since)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %s country tier: %w", spec.Name, err)
	}
	cityAggs, err := s.WindowedAreaAggregates(ctx, cityKinds, since)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %s city tier: %w", spec.Name, err)
	}
	sensors, err := s.WindowedSensors(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %s sensors: %w", spec.Name, err)
	}

	// Everything not listed below is copied from the live snapshot by this
	// assignment and shared with it, which is the intent — see Window. Windows
	// is deliberately left nil: a windowed view has no windows of its own, so
	// Window() on one is the identity and cannot recurse.
	w := *live
	w.Windows = nil

	if w.Overview, err = encode(areaPayloadFrom(now, countryAggs)); err != nil {
		return nil, fmt.Errorf("snapshot: encode %s overview: %w", spec.Name, err)
	}
	if w.OverviewCity, err = encode(areaPayloadFrom(now, cityAggs)); err != nil {
		return nil, fmt.Errorf("snapshot: encode %s city overview: %w", spec.Name, err)
	}
	all := make([]store.AreaAggregate, 0, len(countryAggs)+len(cityAggs))
	all = append(all, countryAggs...)
	all = append(all, cityAggs...)
	if w.Areas, err = encode(areaPayloadFrom(now, all)); err != nil {
		return nil, fmt.Errorf("snapshot: encode %s areas: %w", spec.Name, err)
	}

	// Counted from this window's own fresh set, not copied from live: a 7d
	// window sees sensors a 24h one does not.
	w.coverage = coverageFrom(sensors)
	w.hexTiers = make(map[float64]hexPayload, len(HexTiersKM))
	for _, res := range HexTiersKM {
		p := hexPayloadFrom(now, sensors, res)
		p.Coverage = w.coverage
		w.hexTiers[res] = p
	}
	if w.Hexes, err = encode(w.hexTiers[HexResolutionKM]); err != nil {
		return nil, fmt.Errorf("snapshot: encode %s hexes: %w", spec.Name, err)
	}
	w.points = pointsFrom(sensors)

	// KnownSlugs is shared with the live snapshot, so iterating it here is
	// iterating the same area set — a window cannot invent or lose an area.
	bySlug := make(map[string][]store.SensorReading, len(live.KnownSlugs))
	for _, sr := range sensors {
		for _, slug := range sr.AreaSlugs {
			bySlug[slug] = append(bySlug[slug], sr)
		}
	}
	w.AreaSensors = make(map[string]Body, len(live.KnownSlugs))
	for slug := range live.KnownSlugs {
		body, err := encode(sensorPayloadFrom(now, bySlug[slug]))
		if err != nil {
			return nil, fmt.Errorf("snapshot: encode %s sensors for %q: %w", spec.Name, slug, err)
		}
		w.AreaSensors[slug] = body
	}

	return &w, nil
}
