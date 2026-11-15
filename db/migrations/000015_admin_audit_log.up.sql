-- Persisted admin audit trail: one row per mutating /v1/admin/* request.
-- admin_user_id is NULL for bearer-token (scripts/CI) calls; auth_method
-- records which credential was used ('session' or 'token').
CREATE TABLE t_admin_audit_events (
    id            bigint GENERATED ALWAYS AS IDENTITY,
    admin_user_id uuid        REFERENCES t_admin_users (id),
    auth_method   text        NOT NULL,
    method        text        NOT NULL,
    path          text        NOT NULL,
    status        integer     NOT NULL,
    client_ip     text        NOT NULL,
    request_id    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_audit_events PRIMARY KEY (id)
);

CREATE INDEX idx_t_admin_audit_events_id
    ON t_admin_audit_events (id DESC);
