package api

// The scale tables the frontend colours by. Data, not logic, and served from a
// static endpoint so a legislative change is a one-file edit rather than a
// frontend release.
//
// Sources:
//   - EAQI: European Environment Agency, European Air Quality Index bands for
//     PM10 and PM2.5 (24-hour running mean).
//   - EU limit values: Directive 2008/50/EC — PM10 50 µg/m³ daily,
//     PM2.5 25 µg/m³ annual.
//   - WHO: 2021 Global Air Quality Guidelines — PM10 45 µg/m³ 24-hour,
//     PM2.5 15 µg/m³ 24-hour.
//
// A sensor.community reading is a ~2.5-minute mean from a low-cost nephelometer,
// not a 24-hour reference-method measurement. Bands are therefore INDICATIVE and
// every consumer must say so — Phase 1 §9.2 requires the disclaimer on the page.

type Band struct {
	Label   string `json:"label"`
	LabelBG string `json:"label_bg"`
	// Upper is the inclusive top of the band, or nil for the open-ended top
	// band. A sentinel like 9999 would be a real number a caller could plot.
	Upper  *float64 `json:"upper"`
	Colour string   `json:"colour"`
}

type Scale struct {
	Name    string `json:"name"`
	Metric  string `json:"metric"`
	Unit    string `json:"unit"`
	Bands   []Band `json:"bands"`
	Notes   string `json:"notes"`
	NotesBG string `json:"notes_bg"`
}

func upper(v float64) *float64 { return &v }

// Scales returns every scale table. Recomputed per call rather than shared as a
// package var, because the Band values contain pointers: a shared slice would
// let a caller mutate the table other callers read.
func Scales() []Scale {
	eaqiPM25 := []Band{
		{Label: "Good", LabelBG: "Добро", Upper: upper(5), Colour: "#50f0e6"},
		{Label: "Fair", LabelBG: "Задоволително", Upper: upper(10), Colour: "#50ccaa"},
		{Label: "Moderate", LabelBG: "Умерено", Upper: upper(20), Colour: "#f0e641"},
		{Label: "Poor", LabelBG: "Лошо", Upper: upper(25), Colour: "#ff5050"},
		{Label: "Very poor", LabelBG: "Много лошо", Upper: upper(50), Colour: "#960032"},
		{Label: "Extremely poor", LabelBG: "Изключително лошо", Upper: nil, Colour: "#7d2181"},
	}
	eaqiPM10 := []Band{
		{Label: "Good", LabelBG: "Добро", Upper: upper(20), Colour: "#50f0e6"},
		{Label: "Fair", LabelBG: "Задоволително", Upper: upper(40), Colour: "#50ccaa"},
		{Label: "Moderate", LabelBG: "Умерено", Upper: upper(50), Colour: "#f0e641"},
		{Label: "Poor", LabelBG: "Лошо", Upper: upper(100), Colour: "#ff5050"},
		{Label: "Very poor", LabelBG: "Много лошо", Upper: upper(150), Colour: "#960032"},
		{Label: "Extremely poor", LabelBG: "Изключително лошо", Upper: nil, Colour: "#7d2181"},
	}

	const indicative = "Low-cost sensor readings are indicative and are not " +
		"reference-method measurements."
	const indicativeBG = "Данните от нискобюджетни сензори са индикативни и не " +
		"са измервания по референтен метод."

	return []Scale{
		{Name: "eaqi", Metric: "P2", Unit: "µg/m³", Bands: eaqiPM25,
			Notes:   "European Air Quality Index bands for PM2.5. " + indicative,
			NotesBG: "Класове на Европейския индекс за качество на въздуха за ПМ2.5. " + indicativeBG},
		{Name: "eaqi", Metric: "P1", Unit: "µg/m³", Bands: eaqiPM10,
			Notes:   "European Air Quality Index bands for PM10. " + indicative,
			NotesBG: "Класове на Европейския индекс за качество на въздуха за ПМ10. " + indicativeBG},
		{Name: "eu_limit", Metric: "P1", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the EU daily limit", LabelBG: "В рамките на дневната норма на ЕС", Upper: upper(50), Colour: "#50ccaa"},
				{Label: "Above the EU daily limit", LabelBG: "Над дневната норма на ЕС", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Directive 2008/50/EC: PM10 daily limit 50 µg/m³. " + indicative,
			NotesBG: "Директива 2008/50/ЕО: дневна норма за ПМ10 50 µg/m³. " + indicativeBG},
		{Name: "eu_limit", Metric: "P2", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the EU annual limit", LabelBG: "В рамките на годишната норма на ЕС", Upper: upper(25), Colour: "#50ccaa"},
				{Label: "Above the EU annual limit", LabelBG: "Над годишната норма на ЕС", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Directive 2008/50/EC: PM2.5 annual limit 25 µg/m³. " + indicative,
			NotesBG: "Директива 2008/50/ЕО: годишна норма за ПМ2.5 25 µg/m³. " + indicativeBG},
		{Name: "who", Metric: "P1", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the WHO 24-hour guideline", LabelBG: "В рамките на 24-часовата насока на СЗО", Upper: upper(45), Colour: "#50ccaa"},
				{Label: "Above the WHO 24-hour guideline", LabelBG: "Над 24-часовата насока на СЗО", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "WHO 2021 guidelines: PM10 24-hour 45 µg/m³. " + indicative,
			NotesBG: "Насоки на СЗО 2021: ПМ10 за 24 часа 45 µg/m³. " + indicativeBG},
		{Name: "who", Metric: "P2", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the WHO 24-hour guideline", LabelBG: "В рамките на 24-часовата насока на СЗО", Upper: upper(15), Colour: "#50ccaa"},
				{Label: "Above the WHO 24-hour guideline", LabelBG: "Над 24-часовата насока на СЗО", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "WHO 2021 guidelines: PM2.5 24-hour 15 µg/m³. " + indicative,
			NotesBG: "Насоки на СЗО 2021: ПМ2.5 за 24 часа 15 µg/m³. " + indicativeBG},
	}
}
