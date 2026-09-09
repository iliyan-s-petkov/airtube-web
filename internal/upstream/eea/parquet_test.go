package eea_test

import (
	"io"
	"os"
	"testing"
	"time"

	"airbg.org/internal/upstream/eea"
)

func TestDecodeRowsReadsARealFile(t *testing.T) {
	f, err := os.Open("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}

	rows, err := eea.DecodeRows(f, st.Size())
	if err != nil {
		t.Fatalf("DecodeRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows decoded")
	}

	r := rows[0]
	if r.Samplingpoint == "" {
		t.Error("Samplingpoint is empty")
	}
	if r.Pollutant != 6001 {
		t.Errorf("Pollutant = %d, want 6001", r.Pollutant)
	}
	// A misdecoded int96 lands in 1970 or in the far future; a range check
	// catches both.
	if r.Start.Before(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) ||
		r.Start.After(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Start = %v, outside the plausible range", r.Start)
	}
	if !r.End.After(r.Start) {
		t.Errorf("End %v is not after Start %v", r.End, r.Start)
	}
	// Exact value pinned against an independent decode of the same file with
	// pyarrow; a one-day Julian offset error shifts both Start and End
	// together, so the range and ordering checks above miss it.
	wantStart := time.Date(2025, 1, 1, 8, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)
	if !r.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", r.Start, wantStart)
	}
	if !r.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", r.End, wantEnd)
	}
	// DECIMAL(38,18) decoded wrong is off by a factor of 1e18 either way.
	if r.Value < 0 || r.Value > 5000 {
		t.Errorf("Value = %v, outside the plausible µg/m³ range", r.Value)
	}
	if r.Value != 2.39 {
		t.Errorf("Value = %v, want 2.39", r.Value)
	}
	if r.Unit != "ug.m-3" {
		t.Errorf("Unit = %q, want ug.m-3", r.Unit)
	}
	if r.Validity != 1 && r.Validity != -1 {
		t.Errorf("Validity = %d, want 1 or -1", r.Validity)
	}
}

func TestDecodeRowsRejectsGarbage(t *testing.T) {
	junk := []byte("this is not a parquet file at all, not even close")
	if _, err := eea.DecodeRows(bytesReaderAt(junk), int64(len(junk))); err == nil {
		t.Error("DecodeRows accepted a non-Parquet payload")
	}
}

type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
