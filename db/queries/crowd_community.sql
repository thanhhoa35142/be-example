-- name: AggregateCommunityCrowd :many
-- A slot needs at least $1 DISTINCT devices (n_reports = distinct devices),
-- and each device counts once: its own reports are averaged first, so one
-- device (or a few fake ones) cannot dominate or unlock a slot.
SELECT spot_id, local_dow, local_hour,
       count(*) AS n_reports,
       round(avg(device_level)::numeric, 2) AS avg_level
FROM (
    SELECT spot_id, local_dow, local_hour, device_id,
           avg(level) AS device_level
    FROM t_crowd_reports
    WHERE created_at >= now() - interval '30 days'
    GROUP BY spot_id, local_dow, local_hour, device_id
) per_device
GROUP BY spot_id, local_dow, local_hour
HAVING count(*) >= $1;

-- name: UpsertCommunityEstimate :exec
INSERT INTO t_crowd_estimates (spot_id, dow, hour, level, source, n_reports, updated_at)
VALUES ($1, $2, $3, $4, 'community', $5, now())
ON CONFLICT (spot_id, dow, hour) DO UPDATE SET
    level      = EXCLUDED.level,
    source     = EXCLUDED.source,
    n_reports  = EXCLUDED.n_reports,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteStaleCommunityEstimates :exec
DELETE FROM t_crowd_estimates
WHERE source = 'community'
  AND updated_at < now() - interval '35 days';
