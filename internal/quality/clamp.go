package quality

// IsClamped reports an instrument pegged at the top of its own scale. Matched
// by exact equality, and not covered by InRange. See README.md.
func (s *Scorer) IsClamped(metric string, value float64) bool {
	sentinel, ok := s.cfg.ClampSentinels[metric]
	return ok && value == sentinel
}
