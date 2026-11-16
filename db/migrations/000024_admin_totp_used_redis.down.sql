CREATE TABLE t_admin_totp_used (
    admin_user_id uuid        NOT NULL,
    step          bigint      NOT NULL,
    used_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_t_admin_totp_used          PRIMARY KEY (admin_user_id, step),
    CONSTRAINT fk_t_admin_totp_used_user     FOREIGN KEY (admin_user_id)
        REFERENCES t_admin_users (id) ON DELETE CASCADE
);
