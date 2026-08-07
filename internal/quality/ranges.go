package quality

type valueRange struct{ min, max float64 }

// Physical plausibility bounds (spec §6.1). The SDS011 saturates near
// 999 µg/m³, so readings at the ceiling are meaningless rather than extreme;
// the bound is set at 1000 so saturated values are retained but anything beyond
// is rejected outright.
var metricRanges = map[string]valueRange{
	"P1":           {0, 1000},
	"P2":           {0, 1000},
	"temperature":  {-40, 60},
	"humidity":     {0, 100},
	"pressure":     {800, 1100}, // hPa
	"noise_LAeq":   {25, 120},
	"noise_LA_max": {25, 120},
}

func InRange(metric string, value float64) bool {
	r, ok := metricRanges[metric]
	if !ok {
		return false
	}
	return value >= r.min && value <= r.max
}
