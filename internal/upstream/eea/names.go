package eea

import (
	"embed"
	"encoding/json"
)

//go:embed bg_station_names.json
var bgStationNamesFile embed.FS

// bgStationName is one row of the ExEA-to-Bulgarian-name join. See
// README.md for how the table was built and why it is a committed file
// rather than a runtime fetch.
type bgStationName struct {
	EoI     string `json:"eoi"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Type    string `json:"type"`
	Area    string `json:"area"`
}

// bgStationNames is the EoI-code join table, keyed the same way
// Station.Code already is.
var bgStationNames = loadBGStationNames()

func loadBGStationNames() map[string]bgStationName {
	raw, err := bgStationNamesFile.ReadFile("bg_station_names.json")
	if err != nil {
		panic("eea: bg_station_names.json: " + err.Error())
	}
	var rows []bgStationName
	if err := json.Unmarshal(raw, &rows); err != nil {
		panic("eea: bg_station_names.json: " + err.Error())
	}
	out := make(map[string]bgStationName, len(rows))
	for _, r := range rows {
		out[r.EoI] = r
	}
	return out
}

// applyBGStationName overwrites the metadata file's national code with the
// Bulgarian name from the committed join table. A station absent from the
// table (outside the 32 covered on 2026-09-09) keeps the national code it
// already has, so a future station is visible rather than blank.
func applyBGStationName(st Station) Station {
	n, ok := bgStationNames[st.Code]
	if !ok {
		return st
	}
	st.Name = n.Name
	return st
}
