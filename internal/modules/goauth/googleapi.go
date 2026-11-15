package goauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Read-only Google API calls used by the admin console (resource pickers)
// and the traffic sync worker. All take an already-refreshed access token —
// callers obtain it via Service.AccessToken.

const (
	gscSitesURL     = "https://www.googleapis.com/webmasters/v3/sites"
	gscQueryURL     = "https://www.googleapis.com/webmasters/v3/sites/%s/searchAnalytics/query"
	ga4SummariesURL = "https://analyticsadmin.googleapis.com/v1beta/accountSummaries"
	ga4ReportURL    = "https://analyticsdata.googleapis.com/v1beta/properties/%s:runReport"
)

var ErrGoogleAPI = errors.New("google api request failed")

type GSCSite struct {
	SiteURL         string `json:"siteUrl"`
	PermissionLevel string `json:"permissionLevel"`
}

type GA4Property struct {
	// Property is the resource name, e.g. "properties/123456789".
	Property    string `json:"property"`
	DisplayName string `json:"displayName"`
	Account     string `json:"account"`
}

// GSCRow is one searchAnalytics result row. Keys holds the dimension values
// in the same order the request's dimensions were given.
type GSCRow struct {
	Keys        []string
	Clicks      int64
	Impressions int64
	CTR         float64
	Position    float64
}

// GA4Row is one runReport result row for the daily metrics we sync.
type GA4Row struct {
	Date                   string // YYYYMMDD as returned by GA4
	Sessions               int64
	TotalUsers             int64
	NewUsers               int64
	PageViews              int64
	EngagementRate         *float64
	AvgSessionDurationSecs *float64
}

func (s *Service) doJSON(ctx context.Context, method, rawURL, accessToken string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.http.Do(req)
	if err != nil {
		return ErrGoogleAPI
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s %d", ErrGoogleAPI, res.Request.URL.Host, res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: decode", ErrGoogleAPI)
	}
	return nil
}

// ListGSCSites returns every Search Console property the account can read.
func (s *Service) ListGSCSites(ctx context.Context, accessToken string) ([]GSCSite, error) {
	var out struct {
		SiteEntry []GSCSite `json:"siteEntry"`
	}
	if err := s.doJSON(ctx, http.MethodGet, gscSitesURL, accessToken, nil, &out); err != nil {
		return nil, err
	}
	return out.SiteEntry, nil
}

// ListGA4Properties returns GA4 properties across all accounts the user can
// see, via the Admin API accountSummaries endpoint.
func (s *Service) ListGA4Properties(ctx context.Context, accessToken string) ([]GA4Property, error) {
	var out struct {
		AccountSummaries []struct {
			PropertySummaries []struct {
				Property    string `json:"property"`
				DisplayName string `json:"displayName"`
			} `json:"propertySummaries"`
		} `json:"accountSummaries"`
	}
	if err := s.doJSON(ctx, http.MethodGet, ga4SummariesURL+"?pageSize=200", accessToken, nil, &out); err != nil {
		return nil, err
	}
	var props []GA4Property
	for _, acc := range out.AccountSummaries {
		for _, p := range acc.PropertySummaries {
			if p.Property == "" {
				continue
			}
			props = append(props, GA4Property{
				Property:    p.Property,
				DisplayName: p.DisplayName,
			})
		}
	}
	return props, nil
}

// SearchAnalytics queries the Search Console API for one site. dims are GSC
// dimension names ("date", "query", "page", ...); rowLimit caps the result.
func (s *Service) SearchAnalytics(ctx context.Context, accessToken, siteURL string,
	dims []string, start, end time.Time, rowLimit int) ([]GSCRow, error) {

	u := fmt.Sprintf(gscQueryURL, url.PathEscape(siteURL))
	body := map[string]any{
		"startDate":  start.Format("2006-01-02"),
		"endDate":    end.Format("2006-01-02"),
		"dimensions": dims,
		"rowLimit":   rowLimit,
	}
	var out struct {
		Rows []struct {
			Keys        []string `json:"keys"`
			Clicks      int64    `json:"clicks"`
			Impressions int64    `json:"impressions"`
			CTR         float64  `json:"ctr"`
			Position    float64  `json:"position"`
		} `json:"rows"`
	}
	if err := s.doJSON(ctx, http.MethodPost, u, accessToken, body, &out); err != nil {
		return nil, err
	}
	rows := make([]GSCRow, 0, len(out.Rows))
	for _, r := range out.Rows {
		rows = append(rows, GSCRow{
			Keys: r.Keys, Clicks: r.Clicks, Impressions: r.Impressions,
			CTR: r.CTR, Position: r.Position,
		})
	}
	return rows, nil
}

// RunGA4Report pulls daily sessions/users/pageviews for a property over
// [start, end]. propertyID is the numeric id ("properties/123" → "123").
func (s *Service) RunGA4Report(ctx context.Context, accessToken, propertyID string,
	start, end time.Time) ([]GA4Row, error) {

	pid := propertyID
	if len(pid) > len("properties/") && pid[:len("properties/")] == "properties/" {
		pid = pid[len("properties/"):]
	}
	body := map[string]any{
		"dateRanges": []map[string]string{{
			"startDate": start.Format("2006-01-02"),
			"endDate":   end.Format("2006-01-02"),
		}},
		"dimensions": []map[string]string{{"name": "date"}},
		"metrics": []map[string]string{
			{"name": "sessions"},
			{"name": "totalUsers"},
			{"name": "newUsers"},
			{"name": "screenPageViews"},
			{"name": "engagementRate"},
			{"name": "averageSessionDuration"},
		},
	}
	var out struct {
		Rows []struct {
			DimensionValues []struct {
				Value string `json:"value"`
			} `json:"dimensionValues"`
			MetricValues []struct {
				Value string `json:"value"`
			} `json:"metricValues"`
		} `json:"rows"`
	}
	u := fmt.Sprintf(ga4ReportURL, url.PathEscape(pid))
	if err := s.doJSON(ctx, http.MethodPost, u, accessToken, body, &out); err != nil {
		return nil, err
	}
	rows := make([]GA4Row, 0, len(out.Rows))
	for _, r := range out.Rows {
		if len(r.DimensionValues) == 0 || len(r.MetricValues) < 6 {
			continue
		}
		row := GA4Row{Date: r.DimensionValues[0].Value}
		row.Sessions = parseGA4Int(r.MetricValues[0].Value)
		row.TotalUsers = parseGA4Int(r.MetricValues[1].Value)
		row.NewUsers = parseGA4Int(r.MetricValues[2].Value)
		row.PageViews = parseGA4Int(r.MetricValues[3].Value)
		row.EngagementRate = parseGA4Float(r.MetricValues[4].Value)
		row.AvgSessionDurationSecs = parseGA4Float(r.MetricValues[5].Value)
		rows = append(rows, row)
	}
	return rows, nil
}

func parseGA4Int(v string) int64 {
	var n int64
	_, _ = fmt.Sscanf(v, "%d", &n)
	return n
}

func parseGA4Float(v string) *float64 {
	var f float64
	if _, err := fmt.Sscanf(v, "%g", &f); err != nil {
		return nil
	}
	return &f
}
