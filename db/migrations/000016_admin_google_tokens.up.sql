-- Google account link for the admin console (Search Console + GA4 read).
-- One row per admin user; the OAuth refresh token is AES-256-GCM sealed with
-- the same key family as MFA_SECRET_KEYS before it touches this table.
CREATE TABLE t_admin_google_tokens (
    admin_user_id  uuid        NOT NULL,
    google_sub     text        NOT NULL,
    google_email   text        NOT NULL,
    refresh_token  bytea       NOT NULL,
    scopes         text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_google_tokens       PRIMARY KEY (admin_user_id),
    CONSTRAINT fk_t_admin_google_tokens_user  FOREIGN KEY (admin_user_id)
        REFERENCES t_admin_users (id) ON DELETE CASCADE
);
