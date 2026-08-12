package quality

import (
	"math"
	"slices"
)

// minNeighbours, madScale and madThreshold now live in config.Quality
// (airbg.yaml quality.min_neighbours / mad_scale / mad_threshold): the
// smallest sample from which a median and MAD mean anything, the constant
// that converts MAD to a standard-deviation-equivalent, and how many scaled
// MADs constitutes an outlier, respectively.

// smoothFieldFloors lists the metrics whose values vary smoothly across space,
// so neighbouring sensors genuinely should agree. The floor is the smallest
// deviation that may ever be called an outlier, which prevents an unusually
// tight neighbourhood (MAD near zero) from flagging normal variation.
//
// PM is deliberately absent: it is dominated by point sources, so genuine
// extreme local readings are the signal, not noise. See pmSpatialCheck.
var smoothFieldFloors = map[string]float64{
	"temperature": 1.5,
	"humidity":    8,
	"pressure":    3,
}

// PM guard thresholds. A reading must exceed BOTH to be flagged: many times the
// neighbourhood median AND high in absolute terms. This catches a sensor stuck
// at 900 on a street reading 30, while leaving a genuine 200 µg/m³ inversion
// episode untouched (spec §6.3).
const (
	pmRatioThreshold    = 5.0
	pmAbsoluteThreshold = 150.0
)

type Neighbour struct {
	Lon, Lat float64
	Value    float64
}

// MedianAbsoluteDeviation returns the median of values and the median of the
// absolute deviations from that median. Both are robust to up to half the
// sample being invalid, unlike mean and standard deviation, which are dragged
// by the very outlier being detected.
func MedianAbsoluteDeviation(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	median := medianOfSorted(sorted)

	deviations := make([]float64, len(values))
	for i, v := range values {
		deviations[i] = math.Abs(v - median)
	}
	slices.Sort(deviations)
	return median, medianOfSorted(deviations)
}

func medianOfSorted(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// SpatialCheck compares a reading against its neighbours. It never mutates the
// input slice and is otherwise a pure function of its arguments and the
// scorer's configured thresholds.
func (s *Scorer) SpatialCheck(metric string, value float64, neighbours []Neighbour) Flag {
	if len(neighbours) < s.cfg.MinNeighbours {
		return FlagNoNeighbours
	}

	values := make([]float64, len(neighbours))
	for i, n := range neighbours {
		values[i] = n.Value
	}

	if metric == "P1" || metric == "P2" {
		return pmSpatialCheck(value, values)
	}

	floor, isSmooth := smoothFieldFloors[metric]
	if !isSmooth {
		// Noise is neither smooth nor guarded: it is genuinely local and has no
		// meaningful spatial expectation.
		return FlagOK
	}

	median, mad := MedianAbsoluteDeviation(values)
	deviation := math.Abs(value - median)
	limit := math.Max(s.cfg.MADThreshold*s.cfg.MADScale*mad, floor)
	if deviation > limit {
		return FlagSpatialOutlier
	}
	return FlagOK
}

func pmSpatialCheck(value float64, values []float64) Flag {
	median, _ := MedianAbsoluteDeviation(values)
	if median <= 0 {
		return FlagOK
	}
	if value > pmRatioThreshold*median && value > pmAbsoluteThreshold {
		return FlagSpatialOutlier
	}
	return FlagOK
}
