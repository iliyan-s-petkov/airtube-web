package wind

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"airbg.org/internal/config"
)

// Point is a place to ask about, carrying the hex it belongs to so the answer
// can be stored without matching on coordinates. See docs/wind-overlay.md.
type Point struct {
	Q, R     int
	Lon, Lat float64
}

// Forecast is one hex's wind at one hour.
type Forecast struct {
	Q, R      int
	ValidAt   time.Time
	SpeedMS   float64
	Direction float64
}

type Client struct {
	cfg  config.Wind
	http *http.Client
}

func New(cfg config.Wind) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.RequestTimeout}}
}

// apiSeries is one location's slice of the response.
type apiSeries struct {
	Hourly struct {
		Time      []string   `json:"time"`
		Speed     []*float64 `json:"wind_speed_10m"`
		Direction []*float64 `json:"wind_direction_10m"`
	} `json:"hourly"`
}

// Fetch returns forecasts for every point, in batches of PointsPerReq.
//
// A batch that fails aborts the whole call rather than returning a partial
// grid: half a wind field drawn over a full map of hexes reads as calm air in
// the missing half, not as missing data.
func (c *Client) Fetch(ctx context.Context, points []Point) ([]Forecast, error) {
	var out []Forecast
	for start := 0; start < len(points); start += c.cfg.PointsPerReq {
		end := min(start+c.cfg.PointsPerReq, len(points))
		batch, err := c.fetchBatch(ctx, points[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (c *Client) fetchBatch(ctx context.Context, points []Point) ([]Forecast, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.requestURL(points), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "airbg.org collector (+https://airbg.org)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wind: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wind: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("wind: read body: %w", err)
	}
	return Parse(body, points)
}

func (c *Client) requestURL(points []Point) string {
	lats := make([]string, len(points))
	lons := make([]string, len(points))
	for i, p := range points {
		lats[i] = strconv.FormatFloat(p.Lat, 'f', 4, 64)
		lons[i] = strconv.FormatFloat(p.Lon, 'f', 4, 64)
	}
	q := url.Values{
		"latitude":        {strings.Join(lats, ",")},
		"longitude":       {strings.Join(lons, ",")},
		"hourly":          {"wind_speed_10m,wind_direction_10m"},
		"models":          {c.cfg.Model},
		"wind_speed_unit": {"ms"},
		"timezone":        {"UTC"},
		"forecast_hours":  {strconv.Itoa(c.cfg.ForecastHours)},
	}
	return c.cfg.URL + "?" + q.Encode()
}

// normaliseDirection folds the API's 0-360 into the half-open 0-360 the stored
// row requires: the model reports a due northerly as 360, and wind_forecast's
// own CHECK is direction_deg < 360, so an unfolded 360 aborted the entire write
// batch — one hex an hour out of a few hundred took the whole cycle down.
//
// A bearing is modular, so this loses nothing: 360 and 0 name the same
// direction. Folded here, where the provider's convention already lives, rather
// than relaxed in the constraint — the constraint is what makes "0-360" one
// value per direction instead of two.
func normaliseDirection(deg float64) float64 {
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	return d
}

// Parse maps a response onto the points that produced it, by position.
//
// Position is the only correct join. The coordinates in the response are the
// model's grid cell, not the ones asked for, and at 0.25° several hexes share
// one cell — so matching on coordinates would attach one hex's answer to
// another's, or find no match at all. A length mismatch is an error rather than
// a truncation, because a shifted array silently misplaces every vector.
func Parse(payload []byte, points []Point) ([]Forecast, error) {
	var series []apiSeries
	if err := json.Unmarshal(payload, &series); err != nil {
		// A single-location request answers with an object, not an array. That
		// is a shape this caller never asks for, so it is a bug rather than a
		// case to handle.
		return nil, fmt.Errorf("wind: decode: %w", err)
	}
	if len(series) != len(points) {
		return nil, fmt.Errorf("wind: asked for %d locations, got %d", len(points), len(series))
	}

	var out []Forecast
	for i, s := range series {
		h := s.Hourly
		if len(h.Speed) != len(h.Time) || len(h.Direction) != len(h.Time) {
			return nil, fmt.Errorf("wind: location %d has %d timestamps but %d speeds and %d directions",
				i, len(h.Time), len(h.Speed), len(h.Direction))
		}
		for j, ts := range h.Time {
			// A null speed or direction is a gap in the model run. Dropped
			// rather than stored as zero, which would draw as dead calm.
			if h.Speed[j] == nil || h.Direction[j] == nil {
				continue
			}
			// timezone=UTC, and the API's timestamps carry no zone suffix.
			t, err := time.ParseInLocation("2006-01-02T15:04", ts, time.UTC)
			if err != nil {
				return nil, fmt.Errorf("wind: location %d timestamp %q: %w", i, ts, err)
			}
			out = append(out, Forecast{
				Q:         points[i].Q,
				R:         points[i].R,
				ValidAt:   t,
				SpeedMS:   *h.Speed[j],
				Direction: normaliseDirection(*h.Direction[j]),
			})
		}
	}
	return out, nil
}
