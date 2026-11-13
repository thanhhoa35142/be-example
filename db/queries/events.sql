-- name: InsertEvents :exec
INSERT INTO t_events (device_id, name, props)
SELECT $1::uuid, e->>'name', COALESCE(e->'props', '{}'::jsonb)
FROM jsonb_array_elements($2::jsonb) AS e;

-- name: CountEventsForDeviceSince :one
SELECT count(*) FROM t_events
WHERE device_id = $1 AND created_at > $2;

-- name: AdminEventPropBreakdown :many
-- Breakdown of events.props->>prop (optionally split by a second prop).
-- prop/by are bound parameters (jsonb ->> text), never interpolated.
-- Events missing the prop are bucketed under ''.
SELECT COALESCE(props ->> sqlc.arg(prop)::text, '')::text AS value,
       CASE WHEN sqlc.arg(by)::text = '' THEN ''
            ELSE COALESCE(props ->> sqlc.arg(by)::text, '') END::text AS by_value,
       count(*)::bigint AS n,
       count(DISTINCT device_id)::bigint AS devices
FROM t_events
WHERE name = sqlc.arg(name)
  AND created_at >= sqlc.arg(since)::timestamptz
GROUP BY 1, 2
ORDER BY n DESC, value, by_value
LIMIT 500;
