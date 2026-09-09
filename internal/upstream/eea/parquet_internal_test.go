package eea

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/deprecated"
)

// TestDec18Negative pins two's-complement sign recovery: SetBytes alone reads
// the 16 bytes as unsigned, so a negative DECIMAL(38,18) must be corrected.
func TestDec18Negative(t *testing.T) {
	mag := big.NewInt(2390000000000000000) // 2.39 scaled by 1e18
	twoComp := new(big.Int).Add(new(big.Int).Neg(mag), twoToThe128)
	var b [16]byte
	twoComp.FillBytes(b[:])

	got := dec18(b)
	want := -2.39
	if got != want {
		t.Errorf("dec18(negative) = %v, want %v", got, want)
	}
}

func strPtr(s string) *string { return &s }

func decBytesPtr(scaled int64) *[16]byte {
	var b [16]byte
	big.NewInt(scaled).FillBytes(b[:])
	return &b
}

// TestDecodeRowsSkipsNullValue proves the NULL-drop policy: a row with a NULL
// in Samplingpoint, Value, Unit or AggType must not appear in the output, and
// in particular a NULL Value must never surface as a 0.0 reading.
func TestDecodeRowsSkipsNullValue(t *testing.T) {
	rows := []fileRow{
		{
			Samplingpoint: strPtr("BG/OK"),
			Pollutant:     6001,
			Start:         deprecated.Int96{},
			End:           deprecated.Int96{},
			Value:         decBytesPtr(1000000000000000000), // 1.0
			Unit:          strPtr("ug.m-3"),
			AggType:       strPtr("hour"),
			Validity:      1,
			Verification:  1,
		},
		{
			Samplingpoint: strPtr("BG/NULLVAL"),
			Pollutant:     6001,
			Start:         deprecated.Int96{},
			End:           deprecated.Int96{},
			Value:         nil,
			Unit:          strPtr("ug.m-3"),
			AggType:       strPtr("hour"),
			Validity:      1,
			Verification:  1,
		},
	}

	var buf bytes.Buffer
	if err := parquet.Write(&buf, rows); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	decoded, err := DecodeRows(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("DecodeRows: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("got %d rows, want 1 (the NULL Value row must be dropped)", len(decoded))
	}
	if decoded[0].Samplingpoint != "BG/OK" {
		t.Errorf("Samplingpoint = %q, want BG/OK", decoded[0].Samplingpoint)
	}
}
