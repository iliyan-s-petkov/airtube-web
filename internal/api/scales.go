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
//   - WHO Environmental Noise Guidelines for the European Region (2018):
//     road traffic Lden 53 dB, Lnight 45 dB.
//   - Directive 2003/10/EC: 85 dB(A) upper exposure action value.
//
// Temperature, humidity and pressure have no health legislation behind them and
// their tables here claim none: they are weather bands, and they exist because
// a metric with no table is a metric with no unit and no colour — the panel
// printed "19.59" with nothing after it, and the map painted every dot the same
// grey. Their notes say so in both languages.
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
	Name   string `json:"name"`
	Metric string `json:"metric"`
	Unit   string `json:"unit"`
	Bands  []Band `json:"bands"`
	// Ceiling is the top of the DRAWN scale, and is not a band boundary: the
	// top band of every table here is genuinely open-ended in its source, and
	// giving it an Upper would misstate the legislation. A ramp still has to
	// stop somewhere, and without a stated stop the client can only guess one
	// from the width of the band below — which put the top of the bar at
	// 75 µg/m³ for PM2.5, so a winter inversion at 300 painted the same colour
	// as one at 80 and the key printed no number a reader could check.
	Ceiling *float64 `json:"ceiling"`
	Notes   string   `json:"notes"`
	NotesBG string   `json:"notes_bg"`
	// Source is the published guideline the bands come from, so a reader can
	// check the claim instead of taking the colours on trust. Empty for a table
	// that is only an axis: the meteo bands orient a reader and cite nobody.
	Source string `json:"source"`
}

func upper(v float64) *float64 { return &v }

// The top of the drawn ramp for every particulate scale, in µg/m³. Bulgarian
// winter inversions reach the low hundreds, so a ceiling near the observed
// maximum is what keeps those readings distinguishable from an ordinary bad
// day; it is the same ceiling maps.sensor.community draws to.
const pmCeiling = 500

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

	particulate := []Scale{
		{Name: "eaqi", Metric: "P2", Unit: "µg/m³", Bands: eaqiPM25,
			Notes:   "European Air Quality Index bands for PM2.5. " + indicative,
			NotesBG: "Класове на Европейския индекс за качество на въздуха за ПМ2.5. " + indicativeBG,
			Source:  "https://airindex.eea.europa.eu/"},
		{Name: "eaqi", Metric: "P1", Unit: "µg/m³", Bands: eaqiPM10,
			Notes:   "European Air Quality Index bands for PM10. " + indicative,
			NotesBG: "Класове на Европейския индекс за качество на въздуха за ПМ10. " + indicativeBG,
			Source:  "https://airindex.eea.europa.eu/"},
		{Name: "eu_limit", Metric: "P1", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the EU daily limit", LabelBG: "В рамките на дневната норма на ЕС", Upper: upper(50), Colour: "#50ccaa"},
				{Label: "Above the EU daily limit", LabelBG: "Над дневната норма на ЕС", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Directive 2008/50/EC: PM10 daily limit 50 µg/m³. " + indicative,
			NotesBG: "Директива 2008/50/ЕО: дневна норма за ПМ10 50 µg/m³. " + indicativeBG,
			Source:  "https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32008L0050"},
		{Name: "eu_limit", Metric: "P2", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the EU annual limit", LabelBG: "В рамките на годишната норма на ЕС", Upper: upper(25), Colour: "#50ccaa"},
				{Label: "Above the EU annual limit", LabelBG: "Над годишната норма на ЕС", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Directive 2008/50/EC: PM2.5 annual limit 25 µg/m³. " + indicative,
			NotesBG: "Директива 2008/50/ЕО: годишна норма за ПМ2.5 25 µg/m³. " + indicativeBG,
			Source:  "https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32008L0050"},
		{Name: "who", Metric: "P1", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the WHO 24-hour guideline", LabelBG: "В рамките на 24-часовата насока на СЗО", Upper: upper(45), Colour: "#50ccaa"},
				{Label: "Above the WHO 24-hour guideline", LabelBG: "Над 24-часовата насока на СЗО", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "WHO 2021 guidelines: PM10 24-hour 45 µg/m³. " + indicative,
			NotesBG: "Насоки на СЗО 2021: ПМ10 за 24 часа 45 µg/m³. " + indicativeBG,
			Source:  "https://www.who.int/publications/i/item/9789240034228"},
		{Name: "who", Metric: "P2", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the WHO 24-hour guideline", LabelBG: "В рамките на 24-часовата насока на СЗО", Upper: upper(15), Colour: "#50ccaa"},
				{Label: "Above the WHO 24-hour guideline", LabelBG: "Над 24-часовата насока на СЗО", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "WHO 2021 guidelines: PM2.5 24-hour 15 µg/m³. " + indicative,
			NotesBG: "Насоки на СЗО 2021: ПМ2.5 за 24 часа 15 µg/m³. " + indicativeBG,
			Source:  "https://www.who.int/publications/i/item/9789240034228"},
	}

	// Every table above is particulate matter in µg/m³, so they all draw to the
	// same ceiling; set here rather than per entry so a new one cannot ship
	// without one and fall back to a guessed top of scale.
	for i := range particulate {
		particulate[i].Ceiling = upper(pmCeiling)
	}

	// The rest of what the network measures. Each states its own ceiling,
	// because unlike the particulate tables they share no axis: the top of the
	// temperature bar and the top of the pressure bar are different numbers in
	// different units.
	return append(particulate, weather()...)
}

// weather returns the tables for the five non-particulate metrics.
//
// Bulgarian summer maxima reach the low forties and winter minima the low
// negatives, so the temperature ceiling is 45 and the coldest band is open at
// the bottom the way every first band here is. Pressure is drawn around the
// standard atmosphere (1013.25 hPa) with a band either side, humidity across
// the full 0-100 % it is defined on.
func weather() []Scale {
	const orientation = "Weather bands for orientation. They are not a health " +
		"standard, and low-cost sensor readings are indicative."
	const orientationBG = "Метеорологични класове за ориентация. Те не са " +
		"здравен стандарт, а данните от нискобюджетни сензори са индикативни."
	const noiseIndicative = "Low-cost sensor readings are indicative and are " +
		"not reference-method measurements."
	const noiseIndicativeBG = "Данните от нискобюджетни сензори са индикативни " +
		"и не са измервания по референтен метод."

	return []Scale{
		{Name: "meteo", Metric: "temperature", Unit: "°C", Ceiling: upper(45),
			Bands: []Band{
				{Label: "Severe frost", LabelBG: "Силен студ", Upper: upper(-10), Colour: "#313695"},
				{Label: "Frost", LabelBG: "Мраз", Upper: upper(0), Colour: "#4575b4"},
				{Label: "Cold", LabelBG: "Хладно", Upper: upper(10), Colour: "#74add1"},
				{Label: "Mild", LabelBG: "Умерено", Upper: upper(20), Colour: "#fee090"},
				{Label: "Warm", LabelBG: "Топло", Upper: upper(30), Colour: "#f46d43"},
				{Label: "Hot", LabelBG: "Горещо", Upper: nil, Colour: "#a50026"},
			},
			Notes:   "Air temperature in degrees Celsius. " + orientation,
			NotesBG: "Температура на въздуха в градуси Целзий. " + orientationBG},
		{Name: "meteo", Metric: "humidity", Unit: "%", Ceiling: upper(100),
			Bands: []Band{
				{Label: "Very dry", LabelBG: "Много сухо", Upper: upper(30), Colour: "#a6611a"},
				{Label: "Dry", LabelBG: "Сухо", Upper: upper(40), Colour: "#dfc27d"},
				{Label: "Comfortable", LabelBG: "Комфортно", Upper: upper(60), Colour: "#80cdc1"},
				{Label: "Humid", LabelBG: "Влажно", Upper: upper(80), Colour: "#35978f"},
				{Label: "Very humid", LabelBG: "Много влажно", Upper: nil, Colour: "#01665e"},
			},
			Notes:   "Relative humidity. " + orientation,
			NotesBG: "Относителна влажност. " + orientationBG},
		{Name: "meteo", Metric: "pressure", Unit: "hPa", Ceiling: upper(1050),
			Bands: []Band{
				{Label: "Low", LabelBG: "Ниско", Upper: upper(990), Colour: "#4575b4"},
				{Label: "Below average", LabelBG: "Под средното", Upper: upper(1005), Colour: "#91bfdb"},
				{Label: "Average", LabelBG: "Средно", Upper: upper(1020), Colour: "#d9d9d9"},
				{Label: "Above average", LabelBG: "Над средното", Upper: upper(1035), Colour: "#fdae61"},
				{Label: "High", LabelBG: "Високо", Upper: nil, Colour: "#d73027"},
			},
			Notes:   "Barometric pressure; the standard atmosphere is 1013 hPa. " + orientation,
			NotesBG: "Атмосферно налягане; стандартната атмосфера е 1013 hPa. " + orientationBG},
		{Name: "who", Metric: "noise_LAeq", Unit: "dB(A)", Ceiling: upper(100),
			Bands: []Band{
				{Label: "Quiet", LabelBG: "Тихо", Upper: upper(45), Colour: "#50f0e6"},
				{Label: "Within the WHO road-traffic guideline", LabelBG: "В рамките на насоката на СЗО за пътен шум", Upper: upper(53), Colour: "#50ccaa"},
				{Label: "Noisy", LabelBG: "Шумно", Upper: upper(65), Colour: "#f0e641"},
				{Label: "Very noisy", LabelBG: "Много шумно", Upper: upper(75), Colour: "#ff5050"},
				{Label: "Extremely noisy", LabelBG: "Изключително шумно", Upper: nil, Colour: "#960032"},
			},
			Notes:   "WHO 2018 environmental noise guidelines: road traffic Lden 53 dB, night 45 dB. " + noiseIndicative,
			NotesBG: "Насоки на СЗО 2018 за шума в околната среда: пътен шум Lden 53 dB, нощем 45 dB. " + noiseIndicativeBG,
			Source:  "https://www.who.int/europe/publications/i/item/9789289053563"},
		{Name: "peak", Metric: "noise_LA_max", Unit: "dB(A)", Ceiling: upper(120),
			Bands: []Band{
				{Label: "Low", LabelBG: "Ниско", Upper: upper(55), Colour: "#50f0e6"},
				{Label: "Moderate", LabelBG: "Умерено", Upper: upper(70), Colour: "#f0e641"},
				{Label: "High", LabelBG: "Високо", Upper: upper(85), Colour: "#ff5050"},
				{Label: "Very high", LabelBG: "Много високо", Upper: upper(100), Colour: "#960032"},
				{Label: "Extreme", LabelBG: "Екстремно", Upper: nil, Colour: "#7d2181"},
			},
			Notes:   "Loudest sound level in the interval. 85 dB(A) is the EU upper exposure action value (Directive 2003/10/EC). " + noiseIndicative,
			NotesBG: "Най-силното ниво на звука в интервала. 85 dB(A) е горната стойност на експозиция за предприемане на действие в ЕС (Директива 2003/10/ЕО). " + noiseIndicativeBG,
			Source:  "https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32003L0010"},
	}
}
