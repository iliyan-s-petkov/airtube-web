package quality

import (
	"math"
	"testing"
	"time"

	"airbg.org/internal/upstream"
)

// Sofia, and points roughly 1 km apart from it.
func at(id int64, metric string, value float64, lonOffset float64) upstream.Reading {
	return upstream.Reading{
		SensorID:   id,
		SensorType: "BME280",
		Lon:        23.3327 + lonOffset,
		Lat:        42.6957,
		Metric:     metric,
		Value:      value,
		Timestamp:  time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC),
	}
}

func flagOf(t *testing.T, scored []Scored, id int64) Flag {
	t.Helper()
	for _, s := range scored {
		if s.Reading.SensorID == id {
			return s.Flag
		}
	}
	t.Fatalf("sensor %d missing from scored output", id)
	return ""
}

func TestScoreFlagsOutOfRangeBeforeAnythingElse(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", -999, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagOutOfRange {
		t.Errorf("flag = %v, want %v", got, FlagOutOfRange)
	}
}

func TestScoreFlagsTheSpecExample(t *testing.T) {
	// One sensor reporting 22 °C while every neighbour reports about −10 °C.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	scored := Score(readings, NewHistory(12))

	if got := flagOf(t, scored, 1); got != FlagSpatialOutlier {
		t.Errorf("broken sensor flag = %v, want %v", got, FlagSpatialOutlier)
	}
	for _, id := range []int64{2, 3, 4} {
		if got := flagOf(t, scored, id); got != FlagOK {
			t.Errorf("healthy sensor %d flag = %v, want %v", id, got, FlagOK)
		}
	}
}

// TestScoreExcludesOutOfRangeNeighboursFromTheReference pins score.go's
// reference pre-filter (the `if InRange(...)` loop that builds `reference`).
//
// The population is deliberately *majority dead*: four sensors at −999 against
// three healthy ones. That ratio matters, and an earlier version of this test
// did not have it. With a single −999 neighbour, MAD's own robustness absorbs
// the contamination — the contaminated median moves to −10.35 and the limit
// widens with it, so the subject stays FlagOK and the test passed with the
// pre-filter deleted. It pinned nothing.
//
// With four dead neighbours out of seven the arithmetic inverts:
//
//	filtered   neighbours [−10.2 −10.5 −9.5]  median −10.2  MAD 0.3  limit 1.557
//	             subject deviation 0.2  -> FlagOK
//	unfiltered neighbours [… ×4 −999 …]       median −999   MAD 0    limit 1.5 (floor)
//	             subject deviation 989  -> FlagSpatialOutlier
//
// The dead majority pulls the median all the way to −999 *and* collapses MAD to
// zero, so the limit falls back to the smooth-field floor and the healthy sensor
// is condemned as the outlier. Deleting score.go's pre-filter now fails this
// test, which is the whole point: that pre-filter is also what keeps NaN out of
// every neighbour population (a NaN median would silently disable the check for
// the entire neighbourhood), so it is the single guarantee the branch's NaN
// analysis rests on.
func TestScoreExcludesOutOfRangeNeighboursFromTheReference(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
		at(5, "temperature", -999, 0.04),
		at(6, "temperature", -999, 0.05),
		at(7, "temperature", -999, 0.06),
		at(8, "temperature", -999, 0.07),
	}
	scored := Score(readings, NewHistory(12))

	if got := flagOf(t, scored, 1); got != FlagOK {
		t.Errorf("healthy sensor flag = %v, want %v — the dead −999 majority polluted the reference population", got, FlagOK)
	}
	// The healthy neighbours must survive too: if the pre-filter were removed,
	// every one of them is equally far from the −999 median.
	for _, id := range []int64{2, 3, 4} {
		if got := flagOf(t, scored, id); got != FlagOK {
			t.Errorf("healthy sensor %d flag = %v, want %v", id, got, FlagOK)
		}
	}
	// And the dead sensors are still flagged — excluding them from the
	// reference must not also excuse them.
	for _, id := range []int64{5, 6, 7, 8} {
		if got := flagOf(t, scored, id); got != FlagOutOfRange {
			t.Errorf("dead sensor %d flag = %v, want %v", id, got, FlagOutOfRange)
		}
	}
}

// TestScoreFlagsNonFiniteValuesOutOfRange pins that a NaN or ±Inf value
// arriving from upstream (strconv.ParseFloat accepts "nan", "inf", "Infinity",
// so this is reachable from upstream text, not merely theoretical) is flagged
// out_of_range rather than sailing through. InRange fails closed on NaN only
// because every comparison against NaN is false — an incidental property of
// the expression, not something the code states. Pin it here so a refactor to
// `!(value < min || value > max)`, which reads identically and inverts the NaN
// answer, cannot pass.
func TestScoreFlagsNonFiniteValuesOutOfRange(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		readings := []upstream.Reading{
			at(1, "temperature", v, 0),
			at(2, "temperature", -10, 0.01),
			at(3, "temperature", -10.5, 0.02),
			at(4, "temperature", -9.5, 0.03),
		}
		scored := Score(readings, NewHistory(12))
		if got := flagOf(t, scored, 1); got != FlagOutOfRange {
			t.Errorf("value %v: flag = %v, want %v", v, got, FlagOutOfRange)
		}
	}
}

// TestOutOfRangeReadingNeverEntersHistory pins the *ordering* inside scoreOne:
// the InRange check returns before hist.Observe is reached. That ordering is
// why a NaN-frozen sensor can never poison the stuck-detection state, and why
// the NaN cluster is harmless in the live path. Moving Observe above the range
// check — a plausible "observe everything, judge later" refactor — leaves every
// other test in this file green, so assert on the History state directly.
func TestOutOfRangeReadingNeverEntersHistory(t *testing.T) {
	for _, v := range []float64{-999, math.NaN(), math.Inf(1)} {
		hist := NewHistory(12)
		Score([]upstream.Reading{at(1, "temperature", v, 0)}, hist)

		if _, ok := hist.state[1]["temperature"]; ok {
			t.Errorf("value %v: an out-of-range reading was recorded in History — scoreOne must return before Observe", v)
		}
	}
}

func TestScoreReturnsEveryReading(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
	}
	scored := Score(readings, NewHistory(12))
	if len(scored) != len(readings) {
		t.Fatalf("len(scored) = %d, want %d — readings must never be dropped", len(scored), len(readings))
	}
}

func TestScoreIsolatesMetrics(t *testing.T) {
	// A temperature outlier must not affect humidity scoring at the same sensor.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(1, "humidity", 50, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
		at(2, "humidity", 52, 0.01),
		at(3, "humidity", 48, 0.02),
		at(4, "humidity", 51, 0.03),
	}
	scored := Score(readings, NewHistory(12))

	var tempFlag, humFlag Flag
	for _, s := range scored {
		if s.Reading.SensorID != 1 {
			continue
		}
		switch s.Reading.Metric {
		case "temperature":
			tempFlag = s.Flag
		case "humidity":
			humFlag = s.Flag
		}
	}
	if tempFlag != FlagSpatialOutlier {
		t.Errorf("temperature flag = %v, want %v", tempFlag, FlagSpatialOutlier)
	}
	if humFlag != FlagOK {
		t.Errorf("humidity flag = %v, want %v", humFlag, FlagOK)
	}
}

func TestScoreFlagsStuckSensor(t *testing.T) {
	hist := NewHistory(3)
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	var scored []Scored
	for i := 0; i < 3; i++ {
		scored = Score(readings, hist)
	}
	if got := flagOf(t, scored, 1); got != FlagStuck {
		t.Errorf("flag = %v, want %v", got, FlagStuck)
	}
}

func TestPropertyReadingEqualToNeighbourMedianIsNeverFlagged(t *testing.T) {
	// Invariant: a reading identical to its neighbours' median is always OK,
	// for any in-range value and any neighbour count above the minimum.
	for _, base := range []float64{-30, -10, 0, 5, 20, 35, 55} {
		for n := minNeighbours; n <= 12; n++ {
			readings := []upstream.Reading{at(1, "temperature", base, 0)}
			for i := 1; i <= n; i++ {
				readings = append(readings, at(int64(i+1), "temperature", base, float64(i)*0.005))
			}
			scored := Score(readings, NewHistory(1000))
			if got := flagOf(t, scored, 1); got != FlagOK {
				t.Fatalf("base=%v n=%d: flag = %v, want %v", base, n, got, FlagOK)
			}
		}
	}
}

func TestScoreExcludesSelfFromNeighbours(t *testing.T) {
	// Only two other sensors report this metric — below minNeighbours(3), so the
	// correct verdict is FlagNoNeighbours regardless of sensor 1's own value.
	// If the self-exclusion guard were removed, sensor 1 would see itself as a
	// third "neighbour" (distance 0 from itself), the check would run, and its
	// own extreme value would flip the verdict to FlagSpatialOutlier instead.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagNoNeighbours {
		t.Errorf("flag = %v, want %v — sensor 1 appears to be counting itself as a neighbour", got, FlagNoNeighbours)
	}
}

func TestCoLocatedSensorsAreDistinctNeighbours(t *testing.T) {
	// Sensors 1 and 2 share exact coordinates but have distinct IDs. Each has
	// exactly 3 candidate neighbours (the other co-located sensor plus two
	// more), which only clears minNeighbours(3) if the co-located peer counts.
	// If self-exclusion were keyed on coordinates rather than SensorID, each
	// sensor would wrongly treat its co-located peer as "itself" and drop it,
	// leaving only 2 neighbours and producing FlagNoNeighbours instead of the
	// correct FlagOK.
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10, 0), // same lon/lat as sensor 1, different ID
		at(3, "temperature", -10.5, 0.01),
		at(4, "temperature", -9.5, 0.02),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagOK {
		t.Errorf("sensor 1 flag = %v, want %v — co-located sensor 2 must still count as a neighbour", got, FlagOK)
	}
	if got := flagOf(t, scored, 2); got != FlagOK {
		t.Errorf("sensor 2 flag = %v, want %v — co-located sensor 1 must still count as a neighbour", got, FlagOK)
	}
}

func TestHaversineDistanceMatchesIndependentFormula(t *testing.T) {
	// Two corners of Bulgaria's bounding box (spec: lon 22-29, lat 41-45):
	// far apart, and with lon-spread (7°) and lat-spread (4°) different enough
	// that swapping the argument order changes the answer substantially.
	const lon1, lat1 = 22.0, 41.0
	const lon2, lat2 = 29.0, 45.0

	got := haversineMetres(lon1, lat1, lon2, lat2)

	// Independent reference: the spherical law of cosines, hand-derived and
	// computed here directly rather than reusing the haversine formula under
	// test, so a mistake in haversineMetres isn't self-confirming.
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	want := earthRadiusMetres * math.Acos(
		math.Sin(toRad(lat1))*math.Sin(toRad(lat2))+
			math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Cos(toRad(lon2-lon1)),
	)

	const tolerance = 100.0 // metres; loose relative to a ~690km distance, tight enough to catch real bugs
	if diff := math.Abs(got - want); diff > tolerance {
		t.Errorf("haversineMetres(%v,%v,%v,%v) = %v, want %v (±%v)", lon1, lat1, lon2, lat2, got, want, tolerance)
	}

	// Swapping lon/lat order at these coordinates changes the answer by a
	// large margin, confirming the function's result is sensitive to argument
	// order rather than accidentally symmetric.
	swapped := haversineMetres(lat1, lon1, lat2, lon2)
	const minSwapDifference = 50000.0 // metres
	if diff := math.Abs(got - swapped); diff < minSwapDifference {
		t.Errorf("swapped coordinates gave a suspiciously similar distance: got=%v swapped=%v, want difference > %v", got, swapped, minSwapDifference)
	}
}

func TestPropertyAddingMedianNeighbourNeverCausesAFlag(t *testing.T) {
	// Invariant: adding a neighbour equal to the median cannot turn a previously
	// OK reading into a flagged one.
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	if got := flagOf(t, Score(readings, NewHistory(1000)), 1); got != FlagOK {
		t.Fatalf("precondition failed: flag = %v", got)
	}
	for i := 0; i < 20; i++ {
		readings = append(readings, at(int64(100+i), "temperature", -10.2, float64(i)*0.004))
		if got := flagOf(t, Score(readings, NewHistory(1000)), 1); got != FlagOK {
			t.Fatalf("after adding %d median neighbours: flag = %v, want %v", i+1, got, FlagOK)
		}
	}
}
