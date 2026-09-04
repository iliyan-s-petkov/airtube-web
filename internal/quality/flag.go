// Package quality scores readings for plausibility. Bad readings are flagged,
// never discarded: the map shows them greyed out, and aggregates exclude them
// (spec §5.2, §6).
package quality

// Flag matches the quality_flag enum in the database exactly.
type Flag string

const (
	FlagOK Flag = "ok"
	// FlagClamped is an instrument pegged at the top of its own scale, not an
	// implausible value — separate from out_of_range so an operator can tell
	// "this sensor is saturated" from "this sensor is broken". See clamp.go.
	FlagClamped    Flag = "clamped"
	FlagOutOfRange Flag = "out_of_range"
	FlagStuck          Flag = "stuck"
	FlagSpatialOutlier Flag = "spatial_outlier"
	// FlagNoNeighbours records that the spatial check could not run. It is not
	// a failure: the reading displays normally and counts toward aggregates.
	FlagNoNeighbours Flag = "no_neighbours"
)

// Usable reports whether a flagged reading may contribute to an aggregate.
func (f Flag) Usable() bool {
	return f == FlagOK || f == FlagNoNeighbours
}
