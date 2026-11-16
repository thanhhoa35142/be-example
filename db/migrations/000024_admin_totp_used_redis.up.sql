-- TOTP replay counters move to Redis (TTL-bound, ~2min window). The table
-- only ever held steps from the last few minutes anyway.

DROP TABLE t_admin_totp_used;
