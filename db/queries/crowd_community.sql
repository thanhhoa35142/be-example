-- name: AggregateCommunityCrowd :many
SELECT spot_id, local_dow, local_hour,
       count(*) AS n_reports,
       round(avg(level)::numeric, 2) AS avg_level
FROM crowd_report
WHERE created_at >= now() - interval '30 days'
GROUP BY spot_id, local_dow, local_hour
HAVING count(*) >= $1;

-- name: UpsertCommunityEstimate :exec
INSERT INTO crowd_estimate (spot_id, dow, hour, level, source, n_reports, updated_at)
VALUES ($1, $2, $3, $4, 'community', $5, now())
ON CONFLICT (spot_id, dow, hour) DO UPDATE SET
    level      = EXCLUDED.level,
    source     = EXCLUDED.source,
    n_reports  = EXCLUDED.n_reports,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteStaleCommunityEstimates :exec
DELETE FROM crowd_estimate
WHERE source = 'community'
  AND updated_at < now() - interval '35 days';
