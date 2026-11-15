package persistence

import (
	"context"
	"time"
)

type RetentionRepo struct {
	q *Queries
}

func NewRetentionRepo(q *Queries) *RetentionRepo {
	return &RetentionRepo{q: q}
}

const purgeBatch = 10000

type PurgeStats struct {
	WeatherForecasts int64
	SpotScores       int64
	Events           int64
	CrowdReports     int64
	JobRuns          int64
	AlertDeliveries  int64
	PayloadsScrubbed int64
	AdminSessions    int64
	Plans            int64
}

var (
	weatherRetention   = 7 * 24 * time.Hour
	scoreRetention     = 2 * 24 * time.Hour
	eventRetention     = 90 * 24 * time.Hour
	crowdReportMaxAge  = 180 * 24 * time.Hour
	jobRunRetention    = 30 * 24 * time.Hour
	deliveryRetention  = 30 * 24 * time.Hour
	notificationMaxAge = 90 * 24 * time.Hour
	// Plans (and their followers) are kept 30 days after the window ended or
	// the plan was canceled, whichever came first.
	planRetention = 30 * 24 * time.Hour
)

func (r *RetentionRepo) Purge(ctx context.Context) (PurgeStats, error) {
	var st PurgeStats
	now := time.Now().UTC()

	purge := func(before time.Time, batch func(before time.Time) (int64, error)) (int64, error) {
		var total int64
		for {
			n, err := batch(before)
			if err != nil {
				return total, err
			}
			total += n
			if n < purgeBatch {
				return total, nil
			}
		}
	}

	var err error
	if st.WeatherForecasts, err = purge(now.Add(-weatherRetention), func(b time.Time) (int64, error) {
		return r.q.PurgeWeatherForecasts(ctx, PurgeWeatherForecastsParams{Ts: b, Limit: purgeBatch})
	}); err != nil {
		return st, err
	}
	if st.SpotScores, err = purge(now.Add(-scoreRetention), func(b time.Time) (int64, error) {
		return r.q.PurgeSpotScores(ctx, PurgeSpotScoresParams{TsHour: b, Limit: purgeBatch})
	}); err != nil {
		return st, err
	}
	if st.Events, err = purge(now.Add(-eventRetention), func(b time.Time) (int64, error) {
		return r.q.PurgeEvents(ctx, PurgeEventsParams{CreatedAt: b, Limit: purgeBatch})
	}); err != nil {
		return st, err
	}
	if st.CrowdReports, err = purge(now.Add(-crowdReportMaxAge), func(b time.Time) (int64, error) {
		return r.q.PurgeCrowdReports(ctx, PurgeCrowdReportsParams{CreatedAt: b, Limit: purgeBatch})
	}); err != nil {
		return st, err
	}
	if st.JobRuns, err = purge(now.Add(-jobRunRetention), func(b time.Time) (int64, error) {
		return r.q.PurgeJobRuns(ctx, PurgeJobRunsParams{StartedAt: b, Limit: purgeBatch})
	}); err != nil {
		return st, err
	}
	if st.AlertDeliveries, err = purge(now.Add(-deliveryRetention), func(b time.Time) (int64, error) {
		return r.q.PurgeAlertDeliveries(ctx, PurgeAlertDeliveriesParams{WindowStart: b, Limit: purgeBatch})
	}); err != nil {
		return st, err
	}

	if st.Plans, err = purge(now.Add(-planRetention), func(b time.Time) (int64, error) {
		return r.q.PurgePlans(ctx, PurgePlansParams{Before: b, Batch: purgeBatch})
	}); err != nil {
		return st, err
	}

	// Auth tables: expired sessions are tiny but unbounded without a sweep
	// (login creates a row per session). Challenges live in Redis with TTL.
	if st.AdminSessions, err = r.q.DeleteExpiredAdminSessions(ctx); err != nil {
		return st, err
	}

	before := now.Add(-notificationMaxAge)
	for {
		n, err := r.q.ScrubAppleNotificationPayloads(ctx,
			ScrubAppleNotificationPayloadsParams{ReceivedAt: before, Limit: purgeBatch})
		if err != nil {
			return st, err
		}
		st.PayloadsScrubbed += n
		if n < purgeBatch {
			break
		}
	}
	return st, nil
}
