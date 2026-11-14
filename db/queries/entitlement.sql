-- name: UpsertSubscription :execrows
INSERT INTO t_subscriptions (original_transaction_id, product_id, status,
                          environment, ownership, is_trial, auto_renew,
                          expires_at, grace_expires_at, revoked_at,
                          last_signed_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
-- Out-of-order App Store notifications are dropped by only applying updates
-- whose signed_date (Apple's clock, from the signed payload) is newer than the
-- stored one. Revocation/refund is terminal: a later-signed transaction that
-- is not itself revoked can never flip a revoked subscription back to active.
-- Returns the number of rows written (0 = stale/ignored).
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
WHERE t_subscriptions.last_signed_date < EXCLUDED.last_signed_date
  AND NOT (t_subscriptions.status IN ('revoked','refunded')
           AND EXCLUDED.status NOT IN ('revoked','refunded'));

-- name: LinkDeviceSubscription :exec
INSERT INTO t_device_subscriptions (device_id, original_transaction_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: LockSubscription :one
-- Serialises concurrent link attempts for one original_transaction_id.
SELECT original_transaction_id FROM t_subscriptions
WHERE original_transaction_id = $1
FOR UPDATE;

-- name: IsDeviceLinkedToSubscription :one
SELECT EXISTS(SELECT 1 FROM t_device_subscriptions
              WHERE device_id = $1 AND original_transaction_id = $2);

-- name: CountActiveDevicesForSubscription :one
-- Linked devices that are not soft-deleted (deleted devices free their slot).
SELECT count(*) FROM t_device_subscriptions ds
JOIN t_devices d ON d.id = ds.device_id AND d.deleted_at IS NULL
WHERE ds.original_transaction_id = $1;

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
