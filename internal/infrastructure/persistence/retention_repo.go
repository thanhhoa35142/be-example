package persistence

import (
	"context"
	"time"
)

// RetentionRepo deletes or scrubs rows past their retention window, in
// bounded batches so long-running deletes never hold big locks.
type RetentionRepo struct {
	q *Queries
}

func NewRetentionRepo(q *Queries) *RetentionRepo {
	return &RetentionRepo{q: q}
}

// purgeBatch is the max rows touched per DELETE/UPDATE statement.
const purgeBatch = 10000

// PurgeStats reports rows removed per table during one retention run.
type PurgeStats struct {
	WeatherForecasts int64
	SpotScores       int64
	Events           int64
	CrowdReports     int64
	JobRuns          int64
	AlertDeliveries  int64
	PayloadsScrubbed int64
}

// Retention windows (spec §5). Apple notification payloads are scrubbed
// rather than deleted so the audit trail survives.
var (
	weatherRetention   = 7 * 24 * time.Hour
	scoreRetention     = 2 * 24 * time.Hour
	eventRetention     = 90 * 24 * time.Hour
	crowdReportMaxAge  = 180 * 24 * time.Hour
	jobRunRetention    = 30 * 24 * time.Hour
	deliveryRetention  = 30 * 24 * time.Hour
	notificationMaxAge = 90 * 24 * time.Hour
)

// Purge loops each table in batches until nothing older than the cutoff
// remains, then scrubs old apple_notification payloads.
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
