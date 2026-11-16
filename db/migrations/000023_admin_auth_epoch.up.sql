-- auth_epoch invalidates outstanding admin access JWTs when the account's
-- credentials or second factor change (password set/change, disable,
-- 2FA reset). Access tokens embed the epoch at issue time; Authenticate
-- rejects mismatches, so revocation is immediate despite stateless JWTs.

ALTER TABLE t_admin_users
    ADD COLUMN auth_epoch bigint NOT NULL DEFAULT 0;
