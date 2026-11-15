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
