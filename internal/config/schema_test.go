package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAbsentKeyIsNil verifies that a key absent from YAML results in a nil pointer.
func TestAbsentKeyIsNil(t *testing.T) {
	input := `listen:
  addr: ":8080"
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// listen is present but incomplete
	if r.Listen == nil {
		t.Errorf("Listen = nil, want non-nil")
	}
	// MetricsAddr was omitted → must be nil
	if r.Listen.MetricsAddr != nil {
		t.Errorf("MetricsAddr = %v, want nil", r.Listen.MetricsAddr)
	}
	// BaseURL was omitted → must be nil
	if r.Listen.BaseURL != nil {
		t.Errorf("BaseURL = %v, want nil", r.Listen.BaseURL)
	}
	// MaxConns was omitted → must be nil
	if r.Listen.MaxConns != nil {
		t.Errorf("MaxConns = %v, want nil", r.Listen.MaxConns)
	}
}

// TestPresentZeroIsNotNil verifies that a key present in YAML with zero value
// results in a non-nil pointer to zero.
func TestPresentZeroIsNotNil(t *testing.T) {
	input := `listen:
  max_conns: 0
  addr: ""
cache:
  data_max_age: "0s"
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// listen.max_conns: 0 is present → pointer to 0, not nil
	if r.Listen.MaxConns == nil {
		t.Errorf("MaxConns = nil, want non-nil pointer to 0")
	}
	if r.Listen.MaxConns != nil && *r.Listen.MaxConns != 0 {
		t.Errorf("MaxConns = %v, want 0", *r.Listen.MaxConns)
	}

	// listen.addr: "" is present → pointer to "", not nil
	if r.Listen.Addr == nil {
		t.Errorf("Addr = nil, want non-nil pointer to empty string")
	}
	if r.Listen.Addr != nil && *r.Listen.Addr != "" {
		t.Errorf("Addr = %q, want empty string", *r.Listen.Addr)
	}

	// cache.data_max_age: "0s" is present → pointer to Duration(0), not nil
	if r.Cache.DataMaxAge == nil {
		t.Errorf("DataMaxAge = nil, want non-nil pointer to 0s")
	}
}

// TestTopLevelSectionAbsentIsNil verifies that a top-level section omitted
// entirely results in a nil pointer to the struct.
func TestTopLevelSectionAbsentIsNil(t *testing.T) {
	input := `listen:
  addr: ":8080"
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Sections not in YAML should be nil
	if r.Timeouts != nil {
		t.Errorf("Timeouts = %v, want nil", r.Timeouts)
	}
	if r.Database != nil {
		t.Errorf("Database = %v, want nil", r.Database)
	}
	if r.RateLimit != nil {
		t.Errorf("RateLimit = %v, want nil", r.RateLimit)
	}
	if r.Cache != nil {
		t.Errorf("Cache = %v, want nil", r.Cache)
	}
	if r.Upstream != nil {
		t.Errorf("Upstream = %v, want nil", r.Upstream)
	}
	if r.Store != nil {
		t.Errorf("Store = %v, want nil", r.Store)
	}
	if r.Series != nil {
		t.Errorf("Series = %v, want nil", r.Series)
	}
	if r.Quality != nil {
		t.Errorf("Quality = %v, want nil", r.Quality)
	}
	if r.Backfill != nil {
		t.Errorf("Backfill = %v, want nil", r.Backfill)
	}
	if r.Frontend != nil {
		t.Errorf("Frontend = %v, want nil", r.Frontend)
	}
	if r.Tiles != nil {
		t.Errorf("Tiles = %v, want nil", r.Tiles)
	}
}

// TestNestedStructPointers verifies that nested struct pointers work correctly.
func TestNestedStructPointers(t *testing.T) {
	input := `database:
  api_conns: 10
  statement_timeouts:
    default: "30s"
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// database is present → non-nil
	if r.Database == nil {
		t.Errorf("Database = nil, want non-nil")
	}

	// database.api_conns is present → non-nil pointer
	if r.Database.APIConns == nil {
		t.Errorf("APIConns = nil, want non-nil")
	}
	if *r.Database.APIConns != 10 {
		t.Errorf("APIConns = %v, want 10", *r.Database.APIConns)
	}

	// database.collector_conns was omitted → nil
	if r.Database.CollectorConns != nil {
		t.Errorf("CollectorConns = %v, want nil", r.Database.CollectorConns)
	}

	// database.statement_timeouts is present → non-nil
	if r.Database.StatementTimeouts == nil {
		t.Errorf("StatementTimeouts = nil, want non-nil")
	}

	// statement_timeouts.default is present → non-nil
	if r.Database.StatementTimeouts.Default == nil {
		t.Errorf("StatementTimeouts.Default = nil, want non-nil")
	}

	// statement_timeouts.assign was omitted → nil
	if r.Database.StatementTimeouts.Assign != nil {
		t.Errorf("StatementTimeouts.Assign = %v, want nil", r.Database.StatementTimeouts.Assign)
	}
}

// TestRateLimitComplexStructure verifies complex nested structures like ratelimit.
func TestRateLimitComplexStructure(t *testing.T) {
	input := `ratelimit:
  api:
    per_second: 100.5
    burst: 10.0
  enumerate:
    areas_per_window: 50
  shard_count: 4
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if r.RateLimit == nil {
		t.Errorf("RateLimit = nil, want non-nil")
	}

	// api is present
	if r.RateLimit.API == nil {
		t.Errorf("RateLimit.API = nil, want non-nil")
	}
	if r.RateLimit.API.PerSecond == nil {
		t.Errorf("API.PerSecond = nil, want non-nil")
	}
	if *r.RateLimit.API.PerSecond != 100.5 {
		t.Errorf("API.PerSecond = %v, want 100.5", *r.RateLimit.API.PerSecond)
	}

	// TTL was omitted on api
	if r.RateLimit.API.TTL != nil {
		t.Errorf("API.TTL = %v, want nil", r.RateLimit.API.TTL)
	}

	// enumerate is present
	if r.RateLimit.Enumerate == nil {
		t.Errorf("RateLimit.Enumerate = nil, want non-nil")
	}
	if r.RateLimit.Enumerate.AreasPerWindow == nil {
		t.Errorf("Enumerate.AreasPerWindow = nil, want non-nil")
	}
	if *r.RateLimit.Enumerate.AreasPerWindow != 50 {
		t.Errorf("Enumerate.AreasPerWindow = %v, want 50", *r.RateLimit.Enumerate.AreasPerWindow)
	}

	// sensors_per_window was omitted
	if r.RateLimit.Enumerate.SensorsPerWindow != nil {
		t.Errorf("Enumerate.SensorsPerWindow = %v, want nil", r.RateLimit.Enumerate.SensorsPerWindow)
	}

	// shard_count is present
	if r.RateLimit.ShardCount == nil {
		t.Errorf("ShardCount = nil, want non-nil")
	}
	if *r.RateLimit.ShardCount != 4 {
		t.Errorf("ShardCount = %v, want 4", *r.RateLimit.ShardCount)
	}
}

// TestQualityRanges verifies deeply nested pointer structures like quality.ranges.
func TestQualityRanges(t *testing.T) {
	input := `quality:
  min_neighbours: 3
  ranges:
    P1:
      min: 0.0
      max: 500.0
    P2:
      min: 0.0
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if r.Quality == nil {
		t.Errorf("Quality = nil, want non-nil")
	}
	if r.Quality.MinNeighbours == nil {
		t.Errorf("MinNeighbours = nil, want non-nil")
	}

	// Ranges is present
	if r.Quality.Ranges == nil {
		t.Errorf("Quality.Ranges = nil, want non-nil")
	}

	// P1 is present
	if r.Quality.Ranges.P1 == nil {
		t.Errorf("Ranges.P1 = nil, want non-nil")
	}
	if r.Quality.Ranges.P1.Min == nil {
		t.Errorf("P1.Min = nil, want non-nil")
	}
	if *r.Quality.Ranges.P1.Min != 0.0 {
		t.Errorf("P1.Min = %v, want 0.0", *r.Quality.Ranges.P1.Min)
	}

	// P2 is present but incomplete
	if r.Quality.Ranges.P2 == nil {
		t.Errorf("Ranges.P2 = nil, want non-nil")
	}
	if r.Quality.Ranges.P2.Max != nil {
		t.Errorf("P2.Max = %v, want nil (not specified)", r.Quality.Ranges.P2.Max)
	}

	// Temperature was omitted entirely
	if r.Quality.Ranges.Temperature != nil {
		t.Errorf("Ranges.Temperature = %v, want nil", r.Quality.Ranges.Temperature)
	}
}

// TestSeriesPeriodsList verifies that slices work correctly in the schema.
func TestSeriesPeriodsList(t *testing.T) {
	input := `series:
  default_metric: "P1"
  periods:
    - name: "1h"
      window: "1h"
      hourly: true
      max_age: "24h"
    - name: "7d"
      window: "168h"
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if r.Series == nil {
		t.Errorf("Series = nil, want non-nil")
	}
	if r.Series.DefaultMetric == nil {
		t.Errorf("DefaultMetric = nil, want non-nil")
	}

	if len(r.Series.Periods) != 2 {
		t.Errorf("len(Periods) = %d, want 2", len(r.Series.Periods))
	}

	// First period
	if r.Series.Periods[0].Name == nil || *r.Series.Periods[0].Name != "1h" {
		t.Errorf("Periods[0].Name = %v, want \"1h\"", r.Series.Periods[0].Name)
	}
	if r.Series.Periods[0].Hourly == nil || *r.Series.Periods[0].Hourly != true {
		t.Errorf("Periods[0].Hourly = %v, want true", r.Series.Periods[0].Hourly)
	}

	// Second period — hourly was omitted
	if r.Series.Periods[1].Hourly != nil {
		t.Errorf("Periods[1].Hourly = %v, want nil", r.Series.Periods[1].Hourly)
	}
}

// TestFrontendZeroIntValues verifies that zero int values are non-nil pointers.
func TestFrontendZeroIntValues(t *testing.T) {
	input := `frontend:
  zoom_city: 0
  zoom_sensor: 12
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if r.Frontend == nil {
		t.Errorf("Frontend = nil, want non-nil")
	}

	// zoom_city: 0 is present → non-nil pointer to 0
	if r.Frontend.ZoomCity == nil {
		t.Errorf("ZoomCity = nil, want non-nil pointer to 0")
	}
	if *r.Frontend.ZoomCity != 0 {
		t.Errorf("ZoomCity = %v, want 0", *r.Frontend.ZoomCity)
	}

	// zoom_sensor: 12 is present → non-nil pointer to 12
	if r.Frontend.ZoomSensor == nil {
		t.Errorf("ZoomSensor = nil, want non-nil pointer to 12")
	}
	if *r.Frontend.ZoomSensor != 12 {
		t.Errorf("ZoomSensor = %v, want 12", *r.Frontend.ZoomSensor)
	}

	// no_data_colour was omitted → nil
	if r.Frontend.NoDataColour != nil {
		t.Errorf("NoDataColour = %v, want nil", r.Frontend.NoDataColour)
	}
}

// TestEmptyYAMLIsAllNil verifies that completely empty YAML results in all nil fields.
func TestEmptyYAMLIsAllNil(t *testing.T) {
	input := ""

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Every top-level field should be nil
	if r.Listen != nil {
		t.Errorf("Listen = %v, want nil", r.Listen)
	}
	if r.Timeouts != nil {
		t.Errorf("Timeouts = %v, want nil", r.Timeouts)
	}
	if r.Database != nil {
		t.Errorf("Database = %v, want nil", r.Database)
	}
	if r.RateLimit != nil {
		t.Errorf("RateLimit = %v, want nil", r.RateLimit)
	}
	if r.Cache != nil {
		t.Errorf("Cache = %v, want nil", r.Cache)
	}
	if r.Upstream != nil {
		t.Errorf("Upstream = %v, want nil", r.Upstream)
	}
	if r.Store != nil {
		t.Errorf("Store = %v, want nil", r.Store)
	}
	if r.Series != nil {
		t.Errorf("Series = %v, want nil", r.Series)
	}
	if r.Quality != nil {
		t.Errorf("Quality = %v, want nil", r.Quality)
	}
	if r.Backfill != nil {
		t.Errorf("Backfill = %v, want nil", r.Backfill)
	}
	if r.Frontend != nil {
		t.Errorf("Frontend = %v, want nil", r.Frontend)
	}
	if r.Tiles != nil {
		t.Errorf("Tiles = %v, want nil", r.Tiles)
	}
}

// TestTrustedProxyCIDRsList verifies that []string pointers work correctly.
func TestTrustedProxyCIDRsList(t *testing.T) {
	input := `listen:
  trusted_proxy_cidrs:
    - "10.0.0.0/8"
    - "192.168.0.0/16"
`

	var r raw
	err := yaml.Unmarshal([]byte(input), &r)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if r.Listen.TrustedProxyCIDRs == nil {
		t.Errorf("TrustedProxyCIDRs = nil, want non-nil")
	}
	if len(*r.Listen.TrustedProxyCIDRs) != 2 {
		t.Errorf("len(TrustedProxyCIDRs) = %d, want 2", len(*r.Listen.TrustedProxyCIDRs))
	}
	if (*r.Listen.TrustedProxyCIDRs)[0] != "10.0.0.0/8" {
		t.Errorf("TrustedProxyCIDRs[0] = %q, want \"10.0.0.0/8\"", (*r.Listen.TrustedProxyCIDRs)[0])
	}
}
