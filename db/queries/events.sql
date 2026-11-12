-- name: InsertEvents :exec
INSERT INTO event (device_id, name, props)
SELECT $1::uuid, e->>'name', COALESCE(e->'props', '{}'::jsonb)
FROM jsonb_array_elements($2::jsonb) AS e;

-- name: CountEventsForDeviceSince :one
SELECT count(*) FROM event
WHERE device_id = $1 AND created_at > $2;
