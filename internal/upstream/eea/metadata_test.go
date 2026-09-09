package eea_test

import (
	"os"
	"strings"
	"testing"

	"airbg.org/internal/upstream/eea"
)

func TestParseMetadataResolvesBulgarianSamplingPoints(t *testing.T) {
	f, err := os.Open("testdata/metadata_extract.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	md, err := eea.ParseMetadata(f, []string{"BG"})
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	if len(md) == 0 {
		t.Fatal("no stations parsed")
	}

	for sp, st := range md {
		if !strings.HasPrefix(sp, "BG/") {
			t.Errorf("sampling point %q is not Bulgarian; the country filter leaked", sp)
		}
		if st.Code == "" {
			t.Errorf("%s has no EoI code", sp)
		}
		// Bulgaria's bounding box, generously drawn. A swapped lon/lat or a
		// decimal-comma parse lands well outside it.
		if st.Lon < 22 || st.Lon > 29 || st.Lat < 41 || st.Lat > 45 {
			t.Errorf("%s is at %v,%v — outside Bulgaria", sp, st.Lon, st.Lat)
		}
	}
}

func TestParseMetadataRejectsAMissingColumn(t *testing.T) {
	csv := "Countrycode,SamplingPoint\nBG,BG/SPO-BG0070A_06001_100\n"
	if _, err := eea.ParseMetadata(strings.NewReader(csv), []string{"BG"}); err == nil {
		t.Error("ParseMetadata accepted a header with no coordinate columns")
	}
}

func TestLookupMissesAnUnknownSamplingPoint(t *testing.T) {
	md := eea.Metadata{}
	if _, ok := md.Lookup("BG/SPO-NOPE"); ok {
		t.Error("Lookup claimed to know an absent sampling point")
	}
}
