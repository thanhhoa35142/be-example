-- name: UpsertSubscription :exec
INSERT INTO subscription (original_transaction_id, product_id, status,
                          environment, ownership, is_trial, auto_renew,
                          expires_at, grace_expires_at, revoked_at,
                          last_signed_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
    updated_at       = now();

-- name: LinkDeviceSubscription :exec
INSERT INTO device_subscription (device_id, original_transaction_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ActiveSubscriptionForDevice :one
SELECT s.original_transaction_id, s.product_id, s.status, s.environment,
       s.ownership, s.is_trial, s.auto_renew, s.expires_at,
       s.grace_expires_at, s.revoked_at, s.last_signed_date
FROM subscription s
JOIN device_subscription ds
  ON ds.original_transaction_id = s.original_transaction_id
WHERE ds.device_id = $1
  AND s.status IN ('active', 'grace', 'billing_retry')
  AND (s.expires_at IS NULL OR s.expires_at > now()
       OR (s.grace_expires_at IS NOT NULL AND s.grace_expires_at > now()))
ORDER BY s.expires_at DESC NULLS LAST
LIMIT 1;

-- name: InsertAppleNotification :exec
INSERT INTO apple_notification (notification_uuid, type, subtype,
                                original_transaction_id, signed_date,
                                environment, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (notification_uuid) DO NOTHING;

-- name: MarkNotificationProcessed :exec
UPDATE apple_notification SET processed_at = now()
WHERE notification_uuid = $1;
