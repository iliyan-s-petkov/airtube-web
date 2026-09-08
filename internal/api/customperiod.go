package api

import (
	"net/url"
	"sort"
	"time"

	"airbg.org/internal/config"
)

// CustomPeriod means the window is in ?from=/?to= rather than a configured name.
const CustomPeriod = "custom"

// RFC 3339 only: a naive time would be UTC here and local to the reader, which
// is a chart silently shifted by the offset between them.
const customTimeLayout = time.RFC3339

// customWindow resolves ?from=/?to= into a bounded window plus the configured
// period whose resolution fits it; a non-empty msg is the refusal, sent verbatim.
// A custom range chooses WHERE the window sits, never how much it may extract.
func customWindow(cfg config.Series, q url.Values, now time.Time) (since, until time.Time, pd config.Period, msg string) {
	from, ok := parseInstant(q.Get("from"))
	if !ok {
		return time.Time{}, time.Time{}, config.Period{},
			`The "from" parameter must be a time in RFC 3339 form, e.g. 2026-09-07T14:00:00Z.`
	}
	to, ok := parseInstant(q.Get("to"))
	if !ok {
		return time.Time{}, time.Time{}, config.Period{},
			`The "to" parameter must be a time in RFC 3339 form, e.g. 2026-09-08T09:30:00Z.`
	}

	// A range dragged past now means "up to the latest", not an error.
	if to.After(now) {
		to = now
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, config.Period{},
			`The "from" time must be earlier than the "to" time.`
	}

	longest := longestWindow(cfg)
	if to.Sub(from) > longest {
		return time.Time{}, time.Time{}, config.Period{},
			"The requested range is longer than " + longest.String() + ", the longest window this API serves."
	}

	return from, to, periodForSpan(cfg, to.Sub(from)), ""
}

func parseInstant(v string) (time.Time, bool) {
	t, err := time.Parse(customTimeLayout, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// The configured table in ascending window order; PeriodNames is UI order, not
// numeric order.
func periodsByWindow(cfg config.Series) []config.Period {
	out := make([]config.Period, 0, len(cfg.Periods))
	for _, p := range cfg.Periods {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Window < out[j].Window })
	return out
}

func longestWindow(cfg config.Series) time.Duration {
	byWindow := periodsByWindow(cfg)
	if len(byWindow) == 0 {
		return 0
	}
	return byWindow[len(byWindow)-1].Window
}

// The narrowest configured period covering the span. Its hourly flag is
// correctness, not tuning: raw readings are kept 30 days.
func periodForSpan(cfg config.Series, span time.Duration) config.Period {
	byWindow := periodsByWindow(cfg)
	for _, p := range byWindow {
		if p.Window >= span {
			return p
		}
	}
	if len(byWindow) == 0 {
		return config.Period{}
	}
	return byWindow[len(byWindow)-1]
}

// Exposed so the raw/hourly cut-over can be asserted without a database; the
// handler path alone returns the same stub points either way.
func PeriodForSpanForTesting(cfg config.Series, span time.Duration) (time.Duration, bool, time.Duration) {
	p := periodForSpan(cfg, span)
	return p.Window, p.Hourly, p.Bucket
}
