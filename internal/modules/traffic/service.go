package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// windowDays is how far back each sync re-pulls. Google finalizes Search
// Console data with a 2–3 day lag, so a rolling window keeps recent days
// fresh as they settle.
const windowDays = 28

// dimRowLimit caps queries/pages kept per sync window per site.
const dimRowLimit = 500

type Service struct {
	google Google
	repo   Repository
	logger *slog.Logger
	now    func() time.Time
}

func NewService(google Google, repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{google: google, repo: repo, logger: logger, now: time.Now}
}

type Stats struct {
	Accounts int `json:"accounts"`
	Synced   int `json:"synced"`
	GscRows  int `json:"gsc_rows"`
	Ga4Rows  int `json:"ga4_rows"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// Sync pulls the trailing windowDays of GSC + GA4 data for every linked,
// configured Google account. One bad account does not block the others.
func (s *Service) Sync(ctx context.Context) (*Stats, error) {
	if s.google == nil {
		return &Stats{}, nil
	}
	accounts, err := s.google.LinkedAccounts(ctx)
	if err != nil {
		return nil, err
	}

	stats := &Stats{Accounts: len(accounts)}
	end := s.now().UTC()
	start := end.AddDate(0, 0, -windowDays)

	// Dedupe by Google identity: two admins linking the same account would
	// otherwise pull (and pay for) the same data twice.
	seen := map[string]bool{}
	var errs []error
	for _, acc := range accounts {
		if seen[acc.GoogleSub] {
			stats.Skipped++
			continue
		}
		seen[acc.GoogleSub] = true

		if acc.GSCSiteURL == "" && acc.GA4PropertyID == "" {
			stats.Skipped++
			continue
		}

		token, err := s.google.AccessToken(ctx, acc.AdminUserID)
		if err != nil {
			stats.Failed++
			errs = append(errs, fmt.Errorf("%s: access token: %w", acc.GoogleEmail, err))
			continue
		}

		ok := false
		if acc.GSCSiteURL != "" {
			n, err := s.syncGSC(ctx, token, acc.GSCSiteURL, start, end)
			stats.GscRows += n
			if err != nil {
				errs = append(errs, fmt.Errorf("%s gsc %s: %w", acc.GoogleEmail, acc.GSCSiteURL, err))
			} else {
				ok = true
			}
		}
		if acc.GA4PropertyID != "" {
			n, err := s.syncGA4(ctx, token, acc.GA4PropertyID, start, end)
			stats.Ga4Rows += n
			if err != nil {
				errs = append(errs, fmt.Errorf("%s ga4 %s: %w", acc.GoogleEmail, acc.GA4PropertyID, err))
			} else {
				ok = true
			}
		}
		if ok {
			stats.Synced++
		} else {
			stats.Failed++
		}
	}

	if stats.Synced == 0 && len(errs) > 0 {
		return stats, errors.Join(errs...)
	}
	return stats, nil
}

func (s *Service) syncGSC(ctx context.Context, token, siteURL string, start, end time.Time) (int, error) {
	rows, err := s.google.SearchAnalytics(ctx, token, siteURL, []string{"date"}, start, end, 100)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, r := range rows {
		if len(r.Keys) == 0 {
			continue
		}
		day, err := time.Parse("2006-01-02", r.Keys[0])
		if err != nil {
			continue
		}
		if err := s.repo.UpsertGscDaily(ctx, GscDaily{
			Date: day, SiteURL: siteURL,
			Clicks: r.Clicks, Impressions: r.Impressions, CTR: r.CTR, Position: r.Position,
		}); err != nil {
			return written, err
		}
		written++
	}

	// Dimension snapshots cover the whole window (no date dimension), so
	// clear the site/dimension before rewriting to drop vanished keys.
	for _, dim := range []string{"query", "page"} {
		drows, err := s.google.SearchAnalytics(ctx, token, siteURL, []string{dim}, start, end, dimRowLimit)
		if err != nil {
			return written, err
		}
		if err := s.repo.DeleteGscDims(ctx, siteURL, dim); err != nil {
			return written, err
		}
		for _, r := range drows {
			if len(r.Keys) == 0 || r.Keys[0] == "" {
				continue
			}
			if err := s.repo.UpsertGscDim(ctx, GscDim{
				SiteURL: siteURL, Dimension: dim, Key: r.Keys[0],
				Clicks: r.Clicks, Impressions: r.Impressions,
				CTR: r.CTR, Position: r.Position,
				PeriodStart: start, PeriodEnd: end,
			}); err != nil {
				return written, err
			}
			written++
		}
	}
	return written, nil
}

func (s *Service) syncGA4(ctx context.Context, token, propertyID string, start, end time.Time) (int, error) {
	rows, err := s.google.RunGA4Report(ctx, token, propertyID, start, end)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, r := range rows {
		day, err := time.Parse("20060102", r.Date)
		if err != nil {
			continue
		}
		if err := s.repo.UpsertGa4Daily(ctx, Ga4Daily{
			Date: day, PropertyID: propertyID,
			Sessions: r.Sessions, TotalUsers: r.TotalUsers, NewUsers: r.NewUsers,
			PageViews:      r.PageViews,
			EngagementRate: r.EngagementRate, AvgSessionDurationSecs: r.AvgSessionDurationSecs,
		}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// --- admin reads ---

func (s *Service) GscDaily(ctx context.Context, days int) ([]GscDailyPoint, error) {
	since := s.now().UTC().AddDate(0, 0, -days)
	return s.repo.GscDaily(ctx, since)
}

func (s *Service) GscTop(ctx context.Context, dimension string, days, limit int) ([]GscDimRow, *GscDimWindow, error) {
	rows, err := s.repo.GscDims(ctx, dimension, limit)
	if err != nil {
		return nil, nil, err
	}
	win, err := s.repo.GscDimWindow(ctx, dimension)
	if err != nil {
		return nil, nil, err
	}
	return rows, win, nil
}

func (s *Service) Ga4Daily(ctx context.Context, days int) ([]Ga4DailyPoint, Ga4Totals, error) {
	since := s.now().UTC().AddDate(0, 0, -days)
	daily, err := s.repo.Ga4Daily(ctx, since)
	if err != nil {
		return nil, Ga4Totals{}, err
	}
	totals, err := s.repo.Ga4Totals(ctx, since)
	if err != nil {
		return nil, Ga4Totals{}, err
	}
	return daily, totals, nil
}
