package quality

type valueRange struct{ min, max float64 }

// Physical plausibility bounds (spec §6.1). The SDS011 saturates near
// 999 µg/m³, so readings at the ceiling are meaningless rather than extreme;
// the bound is set at 1000 so saturated values are retained but anything beyond
// is rejected outright.
var metricRanges = map[string]valueRange{
	"P1":          {0, 1000},
	"P2":          {0, 1000},
	"temperature": {-40, 60},
	"humidity":    {0, 100},
	// Floor is 650 hPa (~3600 m), not sea-level-plausible 800 hPa (~2000 m).
	// Bulgaria is mountainous — Musala is 2925 m (~715 hPa), and inhabited
	// sites in the Rila and Pirin ranges sit well above 2000 m with sensors
	// reporting from them. An 800 hPa floor silently discarded every such
	// reading as out_of_range, indistinguishable from a broken sensor. 650 hPa
	// sits above any Bulgarian sensor site, so the check still catches genuine
	// nonsense while no longer rejecting real altitude. This deliberately
	// differs from the approved spec's 800 hPa floor (spec §6.1) — live
	// running showed that value to be the defect, not the fix; do not "tidy"
	// this back toward sea level.
	"pressure":     {650, 1100}, // hPa
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
