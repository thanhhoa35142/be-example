-- pg_stat_statements: per-query call count / total / mean time, needed to
-- find the most expensive queries in production (JIT suspects, seq scans).
--
-- Requires shared_preload_libraries=pg_stat_statements (set in
-- deploy/postgres/docker-compose.yml and docker-compose.perf.yml). The
-- guarded DO block keeps this migration a no-op on servers where the
-- library is not preloaded yet — once it is, run manually:
--   CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'pg_stat_statements not created: %', SQLERRM;
END
$$;
