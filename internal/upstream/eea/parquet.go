// Package eea ingests Bulgaria's official reference stations from the European
// Environment Agency's Air Quality Download API. See README.md.
package eea

import (
	"fmt"
	"io"
	"math/big"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/deprecated"
)

// Row is one hourly observation as the file stores it. Only the columns we
// read are declared; parquet-go ignores the rest.
type Row struct {
	Samplingpoint string
	Pollutant     int32
	Start         time.Time
	End           time.Time
	Value         float64
	Unit          string
	AggType       string
	Validity      int32
	Verification  int32
}

// fileRow mirrors the file's own physical types. Start/End are int96 and Value
// is a DECIMAL(38,18) in a fixed_len_byte_array(16); neither has a Go mapping
// parquet-go will do for us. Samplingpoint, Pollutant, Value, Unit and AggType
// are "optional" in the EEA schema, so they are pointers here: a nil
// distinguishes a real NULL from a genuine zero value or empty string.
type fileRow struct {
	Samplingpoint *string          `parquet:"Samplingpoint,optional"`
	Pollutant     *int32           `parquet:"Pollutant,optional"`
	Start         deprecated.Int96 `parquet:"Start"`
	End           deprecated.Int96 `parquet:"End"`
	Value         *[16]byte        `parquet:"Value,optional"`
	Unit          *string          `parquet:"Unit,optional"`
	AggType       *string          `parquet:"AggType,optional"`
	Validity      int32            `parquet:"Validity"`
	Verification  int32            `parquet:"Verification"`
}

// julianEpochDay is the Julian day number of 1970-01-01, the offset an int96
// timestamp's day word is expressed against.
const julianEpochDay = 2440588

// int96Time reads Parquet's deprecated int96 timestamp: nanoseconds-within-day
// in the low two 32-bit words, Julian day number in the third.
func int96Time(v deprecated.Int96) time.Time {
	nanos := int64(uint64(v[0]) | uint64(v[1])<<32)
	return time.Unix((int64(v[2])-julianEpochDay)*86400, nanos).UTC()
}

// twoToThe128 is the modulus for two's-complement sign recovery below.
var twoToThe128 = new(big.Int).Lsh(big.NewInt(1), 128)

// dec18 reads a DECIMAL(38,18) stored big-endian in 16 bytes, two's complement.
// SetBytes alone reads the bytes as unsigned, so a set high bit (a negative
// value) must be corrected by subtracting 2^128.
func dec18(b [16]byte) float64 {
	i := new(big.Int).SetBytes(b[:])
	if b[0]&0x80 != 0 {
		i.Sub(i, twoToThe128)
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(i), big.NewFloat(1e18)).Float64()
	return f
}

// DecodeRows reads a whole EEA Parquet file. size is required by the format:
// the footer is at the end, so the reader must be told where the end is.
func DecodeRows(r io.ReaderAt, size int64) ([]Row, error) {
	raw, err := parquet.Read[fileRow](r, size)
	if err != nil {
		return nil, fmt.Errorf("eea: read parquet: %w", err)
	}
	out := make([]Row, 0, len(raw))
	for _, fr := range raw {
		// Policy: a row with a NULL in any field required to store or identify
		// the reading is unusable and is dropped, not defaulted. A NULL Value
		// must never reach the store as 0, and a NULL Pollutant must never
		// decode as code 0.
		if fr.Samplingpoint == nil || fr.Pollutant == nil || fr.Value == nil || fr.Unit == nil || fr.AggType == nil {
			continue
		}
		out = append(out, Row{
			Samplingpoint: *fr.Samplingpoint,
			Pollutant:     *fr.Pollutant,
			Start:         int96Time(fr.Start),
			End:           int96Time(fr.End),
			Value:         dec18(*fr.Value),
			Unit:          *fr.Unit,
			AggType:       *fr.AggType,
			Validity:      fr.Validity,
			Verification:  fr.Verification,
		})
	}
	return out, nil
}
