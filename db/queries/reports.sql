-- name: InsertSpotReport :exec
INSERT INTO spot_report (spot_id, device_id, type, issue_code, created_day)
VALUES ($1, $2, $3, $4, current_date);

-- name: InsertCrowdReport :exec
INSERT INTO crowd_report (spot_id, device_id, level, local_dow, local_hour)
VALUES ($1, $2, $3, $4, $5);

-- name: CountCrowdReportsSince :one
SELECT count(*) FROM crowd_report
WHERE device_id = $1 AND created_at > $2;

-- name: InsertScoreFeedback :exec
INSERT INTO score_feedback (spot_id, device_id, ts_hour, verdict)
VALUES ($1, $2, $3, $4);
