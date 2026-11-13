#!/usr/bin/env bash
# Restore a pickhour-cloud.dump (ingest-cloud workflow artifact) into the
# local docker Postgres.
#
#   scripts/ingest/restore-local.sh <pickhour-cloud.dump> [db_name]
#   scripts/ingest/restore-local.sh <pickhour-cloud.dump> --replace
#
# Default target is a separate database (picnic_cloud) so the dev DB is
# untouched; point DATABASE_URL at it to try it out. --replace drops and
# recreates the dev database "picnic" (stop the API/worker first).
#
# Env: PG_CONTAINER (default picnic-postgres), PG_USER (default picnic).
set -euo pipefail

DUMP=${1:?usage: restore-local.sh <dump> [db_name|--replace]}
TARGET=${2:-picnic_cloud}
CONTAINER=${PG_CONTAINER:-picnic-postgres}
PGU=${PG_USER:-picnic}

[ -s "$DUMP" ] || { echo "dump not found: $DUMP" >&2; exit 1; }
if [ "$TARGET" = "--replace" ]; then
  TARGET=picnic
  read -r -p "This DROPS the local database '$TARGET'. Type 'yes' to continue: " ok
  [ "$ok" = yes ] || exit 1
fi

psqlc() { docker exec -i "$CONTAINER" psql -U "$PGU" -d postgres -v ON_ERROR_STOP=1 -q "$@"; }

psqlc -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$TARGET' AND pid <> pg_backend_pid();" >/dev/null
psqlc -c "DROP DATABASE IF EXISTS \"$TARGET\";"
psqlc -c "CREATE DATABASE \"$TARGET\" OWNER \"$PGU\";"

# --no-owner: the cloud role may differ from the local one.
docker exec -i "$CONTAINER" pg_restore -U "$PGU" -d "$TARGET" --no-owner --exit-on-error <"$DUMP"
docker exec -i "$CONTAINER" psql -U "$PGU" -d "$TARGET" -c "ANALYZE;" -q

docker exec -i "$CONTAINER" psql -U "$PGU" -d "$TARGET" -c \
  "SELECT p.slug, p.country, count(s.*) FILTER (WHERE s.status = 'active') active, count(s.*) spots
   FROM t_city_packs p LEFT JOIN t_spots s ON s.city_pack_id = p.id GROUP BY 1, 2 ORDER BY 2, 1;"
docker exec -i "$CONTAINER" psql -U "$PGU" -d "$TARGET" -Atc "SELECT 'schema version ' || version FROM schema_migrations;"
echo "restored into database '$TARGET'"
