// Package traffic syncs Search Console and GA4 daily metrics into Postgres
// so the admin console can chart real traffic without calling Google on
// every page load. The google.sync worker job does the pulling; the admin
// API only reads precomputed rows.
package traffic

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/goauth"
)

// Google is the slice of goauth.Service the sync needs: account listing,
// token refresh and the read-only API calls.
type Google interface {
	LinkedAccounts(ctx context.Context) ([]goauth.Account, error)
	AccessToken(ctx context.Context, userID uuid.UUID) (string, error)
	ListGSCSites(ctx context.Context, accessToken string) ([]goauth.GSCSite, error)
	ListGA4Properties(ctx context.Context, accessToken string) ([]goauth.GA4Property, error)
	SearchAnalytics(ctx context.Context, accessToken, siteURL string,
		dims []string, start, end time.Time, rowLimit int) ([]goauth.GSCRow, error)
	RunGA4Report(ctx context.Context, accessToken, propertyID string,
		start, end time.Time) ([]goauth.GA4Row, error)
}

// GscDaily is one day of Search Console totals for a site.
type GscDaily struct {
	Date        time.Time
	SiteURL     string
	Clicks      int64
	Impressions int64
	CTR         float64
	Position    float64
}

// GscDim is one key (query or page) aggregated over the sync window.
type GscDim struct {
	SiteURL     string
	Dimension   string
	Key         string
	Clicks      int64
	Impressions int64
	CTR         float64
	Position    float64
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// Ga4Daily is one day of GA4 totals for a property.
type Ga4Daily struct {
	Date                   time.Time
	PropertyID             string
	Sessions               int64
	TotalUsers             int64
	NewUsers               int64
	PageViews              int64
	EngagementRate         *float64
	AvgSessionDurationSecs *float64
}

// Read models returned to the admin console, aggregated across sites.
type GscDailyPoint struct {
	Date        time.Time `json:"date"`
	Clicks      int64     `json:"clicks"`
	Impressions int64     `json:"impressions"`
	CTR         float64   `json:"ctr"`
	Position    float64   `json:"position"`
}

type GscDimRow struct {
	Key         string  `json:"key"`
	Clicks      int64   `json:"clicks"`
	Impressions int64   `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

type GscDimWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type Ga4DailyPoint struct {
	Date                   time.Time `json:"date"`
	Sessions               int64     `json:"sessions"`
	TotalUsers             int64     `json:"totalUsers"`
	NewUsers               int64     `json:"newUsers"`
	PageViews              int64     `json:"pageViews"`
	EngagementRate         *float64  `json:"engagementRate"`
	AvgSessionDurationSecs *float64  `json:"avgSessionDurationSec"`
}

type Ga4Totals struct {
	Sessions   int64 `json:"sessions"`
	TotalUsers int64 `json:"totalUsers"`
	NewUsers   int64 `json:"newUsers"`
	PageViews  int64 `json:"pageViews"`
}

type Repository interface {
	UpsertGscDaily(ctx context.Context, row GscDaily) error
	DeleteGscDims(ctx context.Context, siteURL, dimension string) error
	UpsertGscDim(ctx context.Context, row GscDim) error
	UpsertGa4Daily(ctx context.Context, row Ga4Daily) error

	GscDaily(ctx context.Context, since time.Time) ([]GscDailyPoint, error)
	GscDims(ctx context.Context, dimension string, limit int) ([]GscDimRow, error)
	GscDimWindow(ctx context.Context, dimension string) (*GscDimWindow, error)
	Ga4Daily(ctx context.Context, since time.Time) ([]Ga4DailyPoint, error)
	Ga4Totals(ctx context.Context, since time.Time) (Ga4Totals, error)
}
