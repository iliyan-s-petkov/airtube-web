package eea

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// Station is one reference station's identity and position.
type Station struct {
	SamplingPoint string
	Code          string
	Name          string
	Lon           float64
	Lat           float64
	Type          string
	Area          string
}

// Metadata is every station we can place, keyed by sampling point in the
// download API's own format: "<Countrycode>/<SamplingPoint>".
type Metadata map[string]Station

// requiredColumns are read by name; a rename upstream then fails the parse
// instead of yielding zero coordinates.
var requiredColumns = []string{
	"Countrycode", "SamplingPoint", "AirQualityStationEoICode",
	"AirQualityStationNatCode", "Longitude", "Latitude",
	"AirQualityStationType", "AirQualityStationArea",
}

// ParseMetadata reads the EEA PanEuropean metadata file, keeping only the
// named countries. Despite its .csv name the file is tab-delimited. See
// README.md on why coordinates come from this separate, frozen file rather
// than the download API.
func ParseMetadata(r io.Reader, countries []string) (Metadata, error) {
	keep := make(map[string]bool, len(countries))
	for _, c := range countries {
		keep[c] = true
	}

	cr := csv.NewReader(r)
	cr.Comma = '\t'
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("eea: metadata header: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	for _, c := range requiredColumns {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("eea: metadata is missing column %q", c)
		}
	}

	at := func(rec []string, col string) string {
		i := idx[col]
		if i >= len(rec) {
			return ""
		}
		return rec[i]
	}

	md := Metadata{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("eea: metadata row: %w", err)
		}
		country := at(rec, "Countrycode")
		if !keep[country] {
			continue
		}
		sp := at(rec, "SamplingPoint")
		if sp == "" {
			continue
		}
		lon, errLon := strconv.ParseFloat(at(rec, "Longitude"), 64)
		lat, errLat := strconv.ParseFloat(at(rec, "Latitude"), 64)
		if errLon != nil || errLat != nil {
			continue // no usable position; the collector counts the misses
		}
		// Key matches the download API's Samplingpoint field, which carries
		// the country prefix that this file's own SamplingPoint column lacks.
		key := country + "/" + sp
		md[key] = Station{
			SamplingPoint: key,
			Code:          at(rec, "AirQualityStationEoICode"),
			Name:          at(rec, "AirQualityStationNatCode"),
			Lon:           lon,
			Lat:           lat,
			Type:          at(rec, "AirQualityStationType"),
			Area:          at(rec, "AirQualityStationArea"),
		}
	}
	return md, nil
}

// Lookup resolves a sampling point to its station; the download API does not
// carry positions.
func (m Metadata) Lookup(samplingPoint string) (Station, bool) {
	st, ok := m[samplingPoint]
	return st, ok
}
