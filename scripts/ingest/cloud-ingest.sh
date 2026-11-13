#!/usr/bin/env bash
# Cloud ingest driver (used by .github/workflows/ingest-cloud.yml; also runs
# locally against any DATABASE_URL).
#
#   scripts/ingest/cloud-ingest.sh <packs.tsv> <selection...>
#
# selection: group keys and/or pack slugs from packs.tsv, or "all".
#
# Pipeline, one Geofabrik extract at a time:
#   download extract N+1 in the background  ┐ overlapped
#   ingest every pack of extract N           ┘
#   delete extract N's raw .pbf + osmium artifacts
# so peak disk is ~2 raw extracts and download time hides behind ingest.
# The worker itself builds the osmium artifacts and deletes the raw .pbf
# after Prepare (see INGEST_STRATEGY §2 disk model).
#
# A failing extract/pack never stops the others. Results land in
# $OUT_DIR/pack-results.tsv; failures in $OUT_DIR/failures.txt (the
# workflow fails the job at the very end, after the dump is uploaded).
#
# Env: DATABASE_URL, INGEST_EXTRACT_DIR (required); WORKER (default
# /tmp/worker); OUT_DIR (default .); GEOFABRIK_BASE (default
# https://download.geofabrik.de).
set -euo pipefail

PACKS_FILE=${1:?usage: cloud-ingest.sh <packs.tsv> <selection...>}
shift
SELECTION=${*:-all}
: "${DATABASE_URL:?DATABASE_URL required}"
: "${INGEST_EXTRACT_DIR:?INGEST_EXTRACT_DIR required}"
WORKER=${WORKER:-/tmp/worker}
OUT_DIR=${OUT_DIR:-.}
GEOFABRIK_BASE=${GEOFABRIK_BASE:-https://download.geofabrik.de}

mkdir -p "$INGEST_EXTRACT_DIR" "$OUT_DIR"
SELECTED="$OUT_DIR/selected-packs.tsv"
RESULTS="$OUT_DIR/pack-results.tsv"
FAILURES="$OUT_DIR/failures.txt"
: >"$FAILURES"
printf 'slug\textract\tstatus\tseconds\n' >"$RESULTS"

log() { printf '[%(%H:%M:%S)T] %s\n' -1 "$*"; }
fail() { log "FAIL: $*"; echo "$*" >>"$FAILURES"; }

# ---------------------------------------------------------------- select
awk -F'\t' -v sel=" $SELECTION " '
  /^#/ || NF == 0 { next }
  NF != 10 { printf "packs.tsv line %d: expected 10 columns, got %d\n", NR, NF > "/dev/stderr"; bad = 1; next }
  { known[$1] = 1; known[$2] = 1 }
  sel ~ / all / || index(sel, " " $1 " ") || index(sel, " " $2 " ") { print }
  END {
    n = split(sel, toks, " ")
    for (i = 1; i <= n; i++) if (toks[i] != "all" && !(toks[i] in known)) {
      printf "unknown group/slug %s\n", toks[i] > "/dev/stderr"; bad = 1
    }
    exit bad
  }' "$PACKS_FILE" >"$SELECTED"

[ -s "$SELECTED" ] || { echo "selection '$SELECTION' matched no packs" >&2; exit 1; }

# The worker resolves <basename(extract)>.osm.pbf — two different extracts
# with one basename would silently ingest the wrong country.
dups=$(cut -f10 "$SELECTED" | sort -u | awk -F/ '{print $NF}' | sort | uniq -d)
[ -z "$dups" ] || { echo "extract basename collision: $dups" >&2; exit 1; }

log "selected $(wc -l <"$SELECTED") packs from $(cut -f10 "$SELECTED" | sort -u | wc -l) extracts"

# ---------------------------------------------------------- create packs
# Idempotent (ON CONFLICT): re-running against a restored DB only adds new
# packs. last_import_at is required for beta packs by a check constraint.
{
  echo "INSERT INTO t_city_packs (slug, name, country, tz, boundary, status, osm_extract, last_import_at) VALUES"
  awk -F'\t' '{
    gsub(/\047/, "\047\047", $3)
    printf "%s(\047%s\047,\047%s\047,\047%s\047,\047%s\047,ST_Multi(ST_MakeEnvelope(%s,%s,%s,%s,4326)),\047beta\047,\047%s\047,\0471970-01-01\047)\n",
      (NR > 1 ? "," : ""), $2, $3, $4, $5, $6, $7, $8, $9, $10
  }' "$SELECTED"
  echo "ON CONFLICT (slug) DO UPDATE SET osm_extract = EXCLUDED.osm_extract, boundary = EXCLUDED.boundary;"
} | psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q

# ------------------------------------------------------------- download
# Ordered unique extracts (file order keeps related packs together).
mapfile -t EXTRACTS < <(cut -f10 "$SELECTED" | awk '!seen[$0]++')

pbf_path() { echo "$INGEST_EXTRACT_DIR/$(basename "$1").osm.pbf"; }

fetch() { # fetch <extract> — atomic: .part then rename, validated header
  local extract=$1 dest part
  dest=$(pbf_path "$extract")
  part="$dest.part"
  [ -s "$dest" ] && return 0
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors \
    -o "$part" "$GEOFABRIK_BASE/$extract.osm.pbf" || { rm -f "$part"; return 1; }
  # -F pbf: the .part suffix hides the format from osmium
  osmium fileinfo -F pbf "$part" >/dev/null || { rm -f "$part"; return 1; }
  mv "$part" "$dest"
}

cleanup_extract() { # raw pbf (if the worker left it) + osmium artifacts
  local base
  base=$(basename "$1")
  rm -f "$INGEST_EXTRACT_DIR/$base".osm.pbf* "$INGEST_EXTRACT_DIR/$base".f*
}

# --------------------------------------------------------------- ingest
next_pid=""
fetch "${EXTRACTS[0]}" >"$OUT_DIR/fetch-0.log" 2>&1 &
next_pid=$!

for i in "${!EXTRACTS[@]}"; do
  extract=${EXTRACTS[$i]}
  fetch_ok=1
  wait "$next_pid" || fetch_ok=0

  # Prefetch the next extract while this one is ingested.
  if (( i + 1 < ${#EXTRACTS[@]} )); then
    fetch "${EXTRACTS[$((i + 1))]}" >"$OUT_DIR/fetch-$((i + 1)).log" 2>&1 &
    next_pid=$!
  fi

  mapfile -t slugs < <(awk -F'\t' -v e="$extract" '$10 == e { print $2 }' "$SELECTED")
  if (( ! fetch_ok )); then
    fail "download $extract ($(tail -c 300 "$OUT_DIR/fetch-$i.log" | tr '\n' ' '))"
    for slug in "${slugs[@]}"; do printf '%s\t%s\tdownload_failed\t0\n' "$slug" "$extract" >>"$RESULTS"; done
    cleanup_extract "$extract"
    continue
  fi
  log "== $extract ($(du -h "$(pbf_path "$extract")" | cut -f1)): ${slugs[*]}"

  for slug in "${slugs[@]}"; do
    start=$SECONDS
    if "$WORKER" -job ingest.osm -pack "$slug" >"$OUT_DIR/ingest-$slug.log" 2>&1; then
      status=ok
      log "   $slug ok in $((SECONDS - start))s"
    else
      status=failed
      fail "ingest $slug: $(grep -m1 -iE 'error|fail' "$OUT_DIR/ingest-$slug.log" | cut -c1-300)"
    fi
    printf '%s\t%s\t%s\t%d\n' "$slug" "$extract" "$status" $((SECONDS - start)) >>"$RESULTS"
  done
  cleanup_extract "$extract"
  df -h "$INGEST_EXTRACT_DIR" | tail -1 | awk '{print "   disk free: " $4}'
done

log "done: $(grep -c $'\tok\t' "$RESULTS" || true) ok, $(wc -l <"$FAILURES") failures"
