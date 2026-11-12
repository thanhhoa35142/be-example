-- name: UpsertSubscription :exec
INSERT INTO t_subscriptions (original_transaction_id, product_id, status,
                          environment, ownership, is_trial, auto_renew,
                          expires_at, grace_expires_at, revoked_at,
                          last_signed_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
-- Out-of-order App Store notifications are dropped by only applying updates
-- whose signed_date is newer than the stored one.
ON CONFLICT (original_transaction_id) DO UPDATE SET
    product_id       = EXCLUDED.product_id,
    status           = EXCLUDED.status,
    environment      = EXCLUDED.environment,
    ownership        = EXCLUDED.ownership,
    is_trial         = EXCLUDED.is_trial,
    auto_renew       = EXCLUDED.auto_renew,
    expires_at       = EXCLUDED.expires_at,
    grace_expires_at = EXCLUDED.grace_expires_at,
    revoked_at       = EXCLUDED.revoked_at,
    last_signed_date = EXCLUDED.last_signed_date,
    updated_at       = now()
WHERE t_subscriptions.last_signed_date < EXCLUDED.last_signed_date;

-- name: LinkDeviceSubscription :exec
INSERT INTO t_device_subscriptions (device_id, original_transaction_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ActiveSubscriptionForDevice :one
SELECT s.original_transaction_id, s.product_id, s.status, s.environment,
       s.ownership, s.is_trial, s.auto_renew, s.expires_at,
       s.grace_expires_at, s.revoked_at, s.last_signed_date
FROM t_subscriptions s
JOIN t_device_subscriptions ds
  ON ds.original_transaction_id = s.original_transaction_id
WHERE ds.device_id = $1
  AND s.status IN ('active', 'grace', 'billing_retry')
  AND (s.expires_at IS NULL OR s.expires_at > now()
       OR (s.grace_expires_at IS NOT NULL AND s.grace_expires_at > now()))
ORDER BY s.expires_at DESC NULLS LAST
LIMIT 1;

-- name: InsertAppleNotification :exec
INSERT INTO t_apple_notifications (notification_uuid, type, subtype,
                                original_transaction_id, signed_date,
                                environment, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (notification_uuid) DO NOTHING;

-- name: SubscriptionLastSignedDate :one
SELECT last_signed_date FROM t_subscriptions
WHERE original_transaction_id = $1;

-- name: MarkNotificationProcessed :exec
UPDATE t_apple_notifications
SET status = 'processed', processed_at = now()
WHERE notification_uuid = $1 AND status IN ('received','failed');

-- name: MarkNotificationIgnored :exec
-- Stale notification (signed_date not newer than the stored subscription).
UPDATE t_apple_notifications
SET status = 'ignored', processed_at = now()
WHERE notification_uuid = $1 AND status IN ('received','failed');

-- name: MarkNotificationFailed :exec
UPDATE t_apple_notifications
SET status = 'failed', attempts = attempts + 1, last_error = $2
WHERE notification_uuid = $1 AND status IN ('received','failed');
