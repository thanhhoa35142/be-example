-- Admin sessions move to JWT access tokens (stateless) + Redis-backed
-- rotating refresh tokens. The DB session table is no longer needed.
--
-- All existing sessions are dropped: users log in once more.

DROP TABLE t_admin_sessions;
