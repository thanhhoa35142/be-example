-- name: InsertEvents :exec
INSERT INTO t_events (device_id, name, props)
SELECT $1::uuid, e->>'name', COALESCE(e->'props', '{}'::jsonb)
FROM jsonb_array_elements($2::jsonb) AS e;

-- name: CountEventsForDeviceSince :one
SELECT count(*) FROM t_events
WHERE device_id = $1 AND created_at > $2;
