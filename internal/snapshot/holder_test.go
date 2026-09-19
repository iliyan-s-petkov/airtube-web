package snapshot

import (
	"testing"
	"time"

	"airbg.org/internal/config"
)

// TestNewHolderRespectesWindArgument proves NewHolder actually uses the wind
// argument passed to it. The constructor argument must be load-bearing, not
// ignored. This test fails if NewHolder ignores the wind argument and always
// uses config.Wind{}.
func TestNewHolderRespectesWindArgument(t *testing.T) {
	series := config.Series{DefaultMetric: "P2", DefaultWindow: time.Hour}

	// Holder with wind disabled
	disabledWind := NewHolder(series, config.Wind{Enabled: false})
	if got := disabledWind.wind; got.Enabled {
		t.Errorf("NewHolder with Enabled=false resulted in wind.Enabled=%v, want false", got.Enabled)
	}

	// Holder with wind enabled and a specific resolution
	enabledConfig := config.Wind{
		Enabled:       true,
		ResolutionDeg: 0.25,
	}
	enabledWind := NewHolder(series, enabledConfig)
	if got := enabledWind.wind; !got.Enabled || got.ResolutionDeg != 0.25 {
		t.Errorf("NewHolder with wind enabled resulted in %+v, want %+v", got, enabledConfig)
	}
}
