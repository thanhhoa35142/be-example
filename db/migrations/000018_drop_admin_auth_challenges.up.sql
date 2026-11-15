-- Login challenges moved to Redis (adminauth.ChallengeStore): they are
-- ephemeral (5-minute TTL), single-use, and benefit from native expiry
-- instead of rows that needed a retention sweep.
DROP TABLE t_admin_auth_challenges;
