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
		Latitude  string `json:"latitude"`
		Longitude string `json:"longitude"`
		Country   string `json:"country"`
	} `json:"location"`
	Sensor struct {
		ID         int64 `json:"id"`
		SensorType struct {
			Name string `json:"name"`
		} `json:"sensor_type"`
	} `json:"sensor"`
	Values []struct {
		ValueType string `json:"value_type"`
		Value     string `json:"value"`
	} `json:"sensordatavalues"`
}

// Normalise converts an upstream payload into canonical readings. It returns the
// readings, the number of entries skipped as unusable, and an error only when
// the payload as a whole cannot be parsed. A single malformed entry never fails
// the batch (spec §10).
func Normalise(payload []byte) ([]Reading, int, error) {
	var entries []apiEntry
	if err := json.Unmarshal(payload, &entries); err != nil {
		return nil, 0, fmt.Errorf("upstream: parse payload: %w", err)
	}

	readings := make([]Reading, 0, len(entries)*2)
	skipped := 0

	for _, e := range entries {
		lat, errLat := strconv.ParseFloat(e.Location.Latitude, 64)
		lon, errLon := strconv.ParseFloat(e.Location.Longitude, 64)
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
			value, err := strconv.ParseFloat(v.Value, 64)
			if err != nil {
				// e.g. signal's "-78 dBm". Drop the value, keep the entry.
				continue
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
