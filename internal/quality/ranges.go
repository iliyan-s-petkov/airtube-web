package quality

// Physical plausibility bounds (spec §6.1) live in airbg.yaml under
// quality.ranges, including the rationale for the 650 hPa pressure floor
// (Bulgaria's mountain sensors would otherwise be discarded as out_of_range).
// See config.Quality.Ranges.

// InRange rejects a reading outside its instrument's plausibility range. A
// metric with no configured range is rejected rather than accepted: an unknown
// metric that flows through unchecked is how bad data reaches an average.
func (s *Scorer) InRange(metric string, value float64) bool {
	r, ok := s.cfg.Ranges[metric]
	if !ok {
		return false
	}
	// NaN fails both comparisons, which is the intended answer.
	return value >= r.Min && value <= r.Max
}
