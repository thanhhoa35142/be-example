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

// OpenMeteo is a free forecast provider needing no API key — used for dev.
// Docs: https://open-meteo.com/en/docs
const openMeteoDefaultURL = "https://api.open-meteo.com/v1/forecast"

type OpenMeteo struct {
	baseURL string
	client  *http.Client
}

func NewOpenMeteo(client *http.Client) *OpenMeteo {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OpenMeteo{
		baseURL: openMeteoDefaultURL,
		client:  client,
	}
}

func (o *OpenMeteo) Name() string { return "open-meteo" }

// Open-Meteo returns variable-length arrays with nulls; decode manually.
type openMeteoRaw struct {
	Hourly struct {
		Time                     []string          `json:"time"`
		Temperature2m            []json.RawMessage `json:"temperature_2m"`
		ApparentTemperature      []json.RawMessage `json:"apparent_temperature"`
		PrecipitationProbability []json.RawMessage `json:"precipitation_probability"`
		WindSpeed10m             []json.RawMessage `json:"wind_speed_10m"`
		UVIndex                  []json.RawMessage `json:"uv_index"`
		CloudCover               []json.RawMessage `json:"cloud_cover"`
	} `json:"hourly"`
}

func (o *OpenMeteo) Forecast(ctx context.Context, lat, lng float64, from, to time.Time) ([]Point, error) {
	q := url.Values{
		"latitude":           {strconv.FormatFloat(lat, 'f', 4, 64)},
		"longitude":          {strconv.FormatFloat(lng, 'f', 4, 64)},
		"hourly":             {"temperature_2m,apparent_temperature,precipitation_probability,wind_speed_10m,uv_index,cloud_cover"},
		"timezone":           {"UTC"},
		"start_date":         {from.UTC().Format("2006-01-02")},
		"end_date":           {to.UTC().Format("2006-01-02")},
		"wind_speed_unit":    {"kmh"},
		"precipitation_unit": {"mm"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open-meteo: %w", err)
	}
	defer resp.Body.Close()
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
