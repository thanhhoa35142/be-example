package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const openMeteoDefaultURL = "https://api.open-meteo.com/v1/forecast"

type OpenMeteo struct {
	baseURL string
	models  string
	client  *http.Client
}

// NewOpenMeteo builds a provider. baseURL defaults to the public API;
// models is appended as the `models=` query parameter when non-empty
// (required for self-hosted instances — `best_match` needs the full
// domain set, e.g. "gfs_global" needs only ncep_gfs013+ncep_gefs*).
func NewOpenMeteo(client *http.Client, baseURL, models string) *OpenMeteo {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = openMeteoDefaultURL
	}
	return &OpenMeteo{
		baseURL: baseURL,
		models:  models,
		client:  client,
	}
}

func (o *OpenMeteo) Name() string { return "open-meteo" }

type openMeteoRaw struct {
	Hourly struct {
		Time                     []string          `json:"time"`
		Temperature2m            []json.RawMessage `json:"temperature_2m"`
		ApparentTemperature      []json.RawMessage `json:"apparent_temperature"`
		PrecipitationProbability []json.RawMessage `json:"precipitation_probability"`
		Precipitation            []json.RawMessage `json:"precipitation"`
		WindSpeed10m             []json.RawMessage `json:"wind_speed_10m"`
		UVIndex                  []json.RawMessage `json:"uv_index"`
		CloudCover               []json.RawMessage `json:"cloud_cover"`
	} `json:"hourly"`
}

func (o *OpenMeteo) Forecast(ctx context.Context, lat, lng float64, from, to time.Time) ([]Point, error) {
	q := url.Values{
		"latitude":           {strconv.FormatFloat(lat, 'f', 4, 64)},
		"longitude":          {strconv.FormatFloat(lng, 'f', 4, 64)},
		"hourly":             {"temperature_2m,apparent_temperature,precipitation_probability,precipitation,wind_speed_10m,uv_index,cloud_cover"},
		"timezone":           {"UTC"},
		"start_date":         {from.UTC().Format("2006-01-02")},
		"end_date":           {to.UTC().Format("2006-01-02")},
		"wind_speed_unit":    {"kmh"},
		"precipitation_unit": {"mm"},
	}
	if o.models != "" {
		q.Set("models", o.models)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open-meteo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("open-meteo: status %d: %s", resp.StatusCode, body)
	}

	var raw openMeteoRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("open-meteo decode: %w", err)
	}

	n := len(raw.Hourly.Time)
	points := make([]Point, 0, n)
	for i := 0; i < n; i++ {
		ts, err := time.Parse("2006-01-02T15:04", raw.Hourly.Time[i])
		if err != nil {
			ts, err = time.Parse(time.RFC3339, raw.Hourly.Time[i])
			if err != nil {
				continue
			}
		}
		points = append(points, Point{
			TS:         ts.UTC(),
			TempC:      jsonFloat(raw.Hourly.Temperature2m, i),
			FeelsLikeC: jsonFloat(raw.Hourly.ApparentTemperature, i),
			PrecipProb: jsonInt(raw.Hourly.PrecipitationProbability, i),
			PrecipMM:   nonNegative(jsonFloat(raw.Hourly.Precipitation, i)),
			WindKmh:    jsonFloat(raw.Hourly.WindSpeed10m, i),
			UV:         jsonFloat(raw.Hourly.UVIndex, i),
			Cloud:      jsonInt(raw.Hourly.CloudCover, i),
		})
	}
	return points, nil
}

func jsonFloat(arr []json.RawMessage, i int) *float64 {
	if i >= len(arr) || string(arr[i]) == "null" {
		return nil
	}
	var v float64
	if err := json.Unmarshal(arr[i], &v); err != nil {
		return nil
	}
	return &v
}

// nonNegative drops impossible negative amounts (the column has a >= 0
// check; one bad value must not fail the whole cell's insert).
func nonNegative(v *float64) *float64 {
	if v == nil || *v < 0 {
		return nil
	}
	return v
}

func jsonInt(arr []json.RawMessage, i int) *int {
	if i >= len(arr) || string(arr[i]) == "null" {
		return nil
	}
	var v int
	if err := json.Unmarshal(arr[i], &v); err != nil {
		return nil
	}
	return &v
}
