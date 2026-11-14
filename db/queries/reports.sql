-- name: InsertSpotReport :exec
INSERT INTO t_spot_reports (spot_id, device_id, type, issue_code)
VALUES ($1, $2, $3, $4);

-- name: InsertCrowdReport :exec
-- One report per device/spot/hour-slot per UTC day (enforced by
-- uq_t_crowd_reports_slot); duplicates are dropped silently.
INSERT INTO t_crowd_reports (spot_id, device_id, level, local_dow, local_hour)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (device_id, spot_id, created_day, local_hour) DO NOTHING;

-- name: CountCrowdReportsSince :one
SELECT count(*) FROM t_crowd_reports
WHERE device_id = $1 AND created_at > $2;

-- name: InsertScoreFeedback :exec
-- Latest verdict wins for a (device, spot, hour) slot.
INSERT INTO t_score_feedbacks (spot_id, device_id, ts_hour, verdict)
VALUES ($1, $2, $3, $4)
ON CONFLICT (device_id, spot_id, ts_hour)
DO UPDATE SET verdict = EXCLUDED.verdict;

-- name: CountSpotReportsSince :one
SELECT count(*) FROM t_spot_reports
WHERE device_id = $1 AND created_at > $2;

-- name: CountScoreFeedbackSince :one
SELECT count(*) FROM t_score_feedbacks
WHERE device_id = $1 AND created_at > $2;
