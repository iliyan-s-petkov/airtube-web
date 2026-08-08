package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// upstreamTimeLayout is sensor.community's timestamp format. It carries no zone
// and is documented as UTC.
const upstreamTimeLayout = "2006-01-02 15:04:05"

// maxPayloadBytes bounds what we will read from upstream, so a malformed or
// hostile response cannot exhaust memory.
const maxPayloadBytes = 64 << 20

type apiEntry struct {
	Timestamp string `json:"timestamp"`
	Location  struct {
		Latitude  json.RawMessage `json:"latitude"`
		Longitude json.RawMessage `json:"longitude"`
		Country   string          `json:"country"`
	} `json:"location"`
	Sensor struct {
		ID         int64 `json:"id"`
		SensorType struct {
			Name string `json:"name"`
		} `json:"sensor_type"`
	} `json:"sensor"`
	Values []struct {
		ValueType string          `json:"value_type"`
		Value     json.RawMessage `json:"value"`
	} `json:"sensordatavalues"`
}

// parseValue accepts upstream's two observed encodings of a numeric field: a
// quoted numeric string (the historical shape, e.g. "23.50") and a bare JSON
// number (seen on some fields, e.g. pressure_at_sealevel: 84409.38, and on
// latitude/longitude for at least one entry in the wild). Anything else (an
// object, array, non-numeric string, null, …) is reported as an error so the
// caller can drop just that value — never the whole entry or the whole
// payload. This is the same tolerance the legacy string-only parser had for
// junk like signal's "-78 dBm", extended to cover any field that changes
// JSON type across entries. Used for sensordatavalues.value as well as
// location.latitude/longitude — the two other fields most likely to drift
// the same way, for the same reason.
func parseValue(raw json.RawMessage) (float64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strconv.ParseFloat(s, 64)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	return 0, fmt.Errorf("value %q is neither a numeric string nor a number", string(raw))
}

// Normalise converts an upstream payload into canonical readings. It returns the
// readings, the number of entries skipped as unusable, and an error only when
// the payload as a whole cannot be parsed (i.e. it is not even a JSON array).
// A single malformed entry never fails the batch (spec §10): decoding happens
// per element, so a structural drift in one entry — a field renamed, retyped
// (e.g. sensor.id sent as a string, latitude sent as an unquoted number), or
// sensordatavalues sent as an object instead of an array — only drops that
// entry, never the rest of the payload.
func Normalise(payload []byte) ([]Reading, int, error) {
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(payload, &rawEntries); err != nil {
		return nil, 0, fmt.Errorf("upstream: parse payload: %w", err)
	}

	readings := make([]Reading, 0, len(rawEntries)*2)
	skipped := 0

	for _, raw := range rawEntries {
		var e apiEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			// Structural drift confined to this one entry (wrong type for a
			// field, sensordatavalues not an array, …). Drop the entry, keep
			// the batch.
			skipped++
			continue
		}

		lat, errLat := parseValue(e.Location.Latitude)
		lon, errLon := parseValue(e.Location.Longitude)
		ts, errTS := time.Parse(upstreamTimeLayout, e.Timestamp)
		if errLat != nil || errLon != nil || errTS != nil || e.Sensor.ID == 0 {
			skipped++
			continue
		}

		emitted := 0
		for _, v := range e.Values {
			if !canonicalMetrics[v.ValueType] {
				continue
			}
			value, err := parseValue(v.Value)
			if err != nil {
				// e.g. signal's "-78 dBm", or a value that arrived as an
				// unparseable type. Drop the value, keep the entry.
				continue
			}
			// Upstream reports pressure in Pascals; canonical storage is hPa.
			if v.ValueType == "pressure" {
				value /= 100
			}
			readings = append(readings, Reading{
				SensorID:   e.Sensor.ID,
				SensorType: e.Sensor.SensorType.Name,
				Lon:        lon,
				Lat:        lat,
				Metric:     v.ValueType,
				Value:      value,
				Timestamp:  ts.UTC(),
			})
			emitted++
		}
		if emitted == 0 {
			skipped++
		}
	}
	return readings, skipped, nil
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Fetch(ctx context.Context) ([]Reading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "airbg.org collector (+https://airbg.org)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream: status %d", resp.StatusCode)
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("upstream: read body: %w", err)
	}

	readings, _, err := Normalise(payload)
	return readings, err
}
