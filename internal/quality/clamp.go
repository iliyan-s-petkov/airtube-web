package quality

// A clamp sentinel is the value an instrument reports when it has run off the
// top of its own scale. The SDS011 pegs at 1999.9 µg/m³ for PM10 and 999.9 for
// PM2.5; both arrive as ordinary readings with no marker of any kind.
//
// This is not a plausibility question, which is why it is not left to InRange.
// 999.9 sits inside the configured P2 range of 0–1000, so the sentinel passed
// the range check, entered the reference population, and dragged the
// neighbourhood median its own neighbours were scored against. Its P1 twin at
// 1999.9 happened to fall outside the P1 range and was caught — so the pair
// was split, one half rejected and one half averaged in.
//
// Matched by exact equality, deliberately. A range would swallow the genuine
// readings just below the peg, and the sentinel is a fixed constant the
// firmware emits verbatim, never a computed value that lands nearby.
func (s *Scorer) IsClamped(metric string, value float64) bool {
	sentinel, ok := s.cfg.ClampSentinels[metric]
	return ok && value == sentinel
}
