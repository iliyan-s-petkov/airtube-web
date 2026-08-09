package quality

import "sync"

// exemptStuckValues are readings that legitimately repeat forever. Flagging them
// would mark healthy sensors as broken (spec §6.2).
var exemptStuckValues = map[string][]float64{
	"humidity": {0, 100},
	"P1":       {0},
	"P2":       {0},
}

type seriesState struct {
	last  float64
	runs  int
	valid bool
}

// History tracks consecutive identical readings per (sensor, metric).
//
// State lives in memory and is empty after a restart, so stuck detection needs
// `depth` cycles (about one hour at a five-minute cadence) to warm up. That is
// acceptable: a stuck sensor stays stuck, so it is detected on the next warm
// window rather than missed.
type History struct {
	mu    sync.Mutex
	depth int
	state map[int64]map[string]*seriesState
}

func NewHistory(depth int) *History {
	return &History{
		depth: depth,
		state: make(map[int64]map[string]*seriesState),
	}
}

func (h *History) Observe(sensorID int64, metric string, value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	byMetric, ok := h.state[sensorID]
	if !ok {
		byMetric = make(map[string]*seriesState)
		h.state[sensorID] = byMetric
	}
	s, ok := byMetric[metric]
	if !ok {
		byMetric[metric] = &seriesState{last: value, runs: 1, valid: true}
		return
	}
	if s.valid && s.last == value {
		s.runs++
		return
	}
	s.last = value
	s.runs = 1
	s.valid = true
}

func (h *History) IsStuck(sensorID int64, metric string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.state[sensorID][metric]
	if !ok || s.runs < h.depth {
		return false
	}
	for _, exempt := range exemptStuckValues[metric] {
		if s.last == exempt {
			return false
		}
	}
	return true
}
