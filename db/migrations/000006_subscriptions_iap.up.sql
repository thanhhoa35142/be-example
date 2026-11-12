-- Domain: in-app purchase (StoreKit 2 / App Store Server Notifications)

CREATE TABLE subscription (
    original_transaction_id TEXT PRIMARY KEY,
    product_id              TEXT NOT NULL,
    status                  TEXT NOT NULL CHECK (status IN
                            ('active', 'grace', 'billing_retry', 'expired', 'revoked', 'refunded')),
    environment             TEXT NOT NULL CHECK (environment IN ('production', 'sandbox')),
    ownership               TEXT NOT NULL CHECK (ownership IN ('purchased', 'family_shared')),
    is_trial                BOOLEAN NOT NULL DEFAULT false,
    auto_renew              BOOLEAN NOT NULL DEFAULT true,
    expires_at              TIMESTAMPTZ,
    grace_expires_at        TIMESTAMPTZ,
    revoked_at              TIMESTAMPTZ,
    last_signed_date        TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE device_subscription (
    device_id                UUID NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    original_transaction_id  TEXT NOT NULL REFERENCES subscription(original_transaction_id),
    linked_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, original_transaction_id)
);

CREATE TABLE apple_notification (
    notification_uuid        UUID PRIMARY KEY,
    type                     TEXT NOT NULL,
    subtype                  TEXT,
    original_transaction_id  TEXT,
    signed_date              TIMESTAMPTZ NOT NULL,
    environment              TEXT NOT NULL,
    payload                  JSONB NOT NULL,
    received_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at             TIMESTAMPTZ
);

CREATE TABLE plan_limit (
    plan         TEXT CHECK (plan IN ('free', 'plus')),
    feature      TEXT,
    limit_value  INT,
    PRIMARY KEY (plan, feature)
);
