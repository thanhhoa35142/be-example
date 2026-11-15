-- Admin console accounts: email + argon2id password + TOTP 2FA.
-- No self sign-up: rows are created only by the staff CLI (cmd/admin).
-- TOTP secrets and pending-enrollment secrets are AES-256-GCM encrypted
-- (key from MFA_SECRET_KEYS) before they touch this table.
CREATE TABLE t_admin_users (
    id                uuid        DEFAULT uuidv7() NOT NULL,
    email             text        NOT NULL,
    password_hash     text        NOT NULL,
    totp_secret       bytea,
    totp_confirmed_at  timestamptz,
    disabled_at       timestamptz,
    last_login_at     timestamptz,
    failed_attempts   int         NOT NULL DEFAULT 0,
    locked_until      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_users            PRIMARY KEY (id),
    CONSTRAINT uq_t_admin_users_email      UNIQUE (email),
    CONSTRAINT ck_t_admin_users_email      CHECK (email = lower(email) AND position('@' in email) > 1),
    CONSTRAINT ck_t_admin_users_failures   CHECK (failed_attempts >= 0)
);

-- Server-side sessions. The cookie carries an opaque random token; only its
-- SHA-256 hash is stored. 12 h absolute expiry (expires_at) + 30 min idle
-- (last_seen_at, enforced by the auth middleware).
CREATE TABLE t_admin_sessions (
    id            uuid        DEFAULT uuidv7() NOT NULL,
    token_hash    bytea       NOT NULL,
    admin_user_id uuid        NOT NULL,
    user_agent    text        NOT NULL DEFAULT '',
    ip            text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_sessions               PRIMARY KEY (id),
    CONSTRAINT uq_t_admin_sessions_token_hash    UNIQUE (token_hash),
    CONSTRAINT fk_t_admin_sessions_user          FOREIGN KEY (admin_user_id)
        REFERENCES t_admin_users (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_admin_sessions_expiry        CHECK (expires_at > created_at)
);
CREATE INDEX ix_t_admin_sessions_user ON t_admin_sessions (admin_user_id);
CREATE INDEX ix_t_admin_sessions_expiry ON t_admin_sessions (expires_at);

-- Short-lived (5 min) single-use pending challenges between the password
-- step and the TOTP step. kind='login' awaits a code; kind='enroll' carries
-- the pending (still unconfirmed) TOTP secret for first-login enrollment.
CREATE TABLE t_admin_auth_challenges (
    token_hash    bytea       NOT NULL,
    admin_user_id uuid        NOT NULL,
    kind          text        NOT NULL,
    totp_secret   bytea,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_auth_challenges          PRIMARY KEY (token_hash),
    CONSTRAINT fk_t_admin_auth_challenges_user     FOREIGN KEY (admin_user_id)
        REFERENCES t_admin_users (id) ON DELETE CASCADE,
    CONSTRAINT ck_t_admin_auth_challenges_kind     CHECK (kind IN ('login','enroll')),
    CONSTRAINT ck_t_admin_auth_challenges_expiry   CHECK (expires_at > created_at)
);

-- One-time recovery codes; only the SHA-256 hash is stored.
CREATE TABLE t_admin_recovery_codes (
    id            uuid        DEFAULT uuidv7() NOT NULL,
    admin_user_id uuid        NOT NULL,
    code_hash     bytea       NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    used_at       timestamptz,
    CONSTRAINT pk_t_admin_recovery_codes            PRIMARY KEY (id),
    CONSTRAINT uq_t_admin_recovery_codes_hash        UNIQUE (code_hash),
    CONSTRAINT fk_t_admin_recovery_codes_user        FOREIGN KEY (admin_user_id)
        REFERENCES t_admin_users (id) ON DELETE CASCADE
);
CREATE INDEX ix_t_admin_recovery_codes_user ON t_admin_recovery_codes (admin_user_id)
    WHERE used_at IS NULL;

-- TOTP counters already accepted, for replay rejection across the ±1 skew.
CREATE TABLE t_admin_totp_used (
    admin_user_id uuid        NOT NULL,
    step          bigint      NOT NULL,
    used_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_totp_used          PRIMARY KEY (admin_user_id, step),
    CONSTRAINT fk_t_admin_totp_used_user     FOREIGN KEY (admin_user_id)
        REFERENCES t_admin_users (id) ON DELETE CASCADE
);
