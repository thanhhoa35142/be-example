-- Restore the pre-JWT session table (single opaque token, 12h + idle).

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
