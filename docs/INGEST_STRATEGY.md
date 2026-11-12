# Ingest data strategy — spots from OSM (and beyond)

Status: **implemented & verified at pack scale** (Luxembourg, Belgium,
Netherlands extracts through osmium). Remaining gaps listed in
§10. This doc is the source of truth for the ingest module; update it
when decisions change.

## Bottom line

Two layers:

- **Raw layer** (`t_ingest_features`): last OSM snapshot per city pack —
  every classified feature with tags, geometry, content hash. Audit +
  re-planning without re-downloading.
- **Canonical layer** (`t_spots` + `t_spot_sources` + `t_spot_amenities`):
  what the product serves.

Heavy I/O (download, osmium) runs **outside** the DB transaction.
Reconciliation is **one short transaction per pack** with a **pure Go
planner** in the middle → imports are idempotent, dry-runnable and
golden-testable.

**Spot IDs are sacred; sources are volatile.** Alert rules, reports and
crowd history all hang off `t_spots.id`, so re-imports must re-link
existing spots instead of recreating them.

## Production path

```
Geofabrik .pbf → osmium CLI → GeoJSON stream → Go classify → raw layer
             → PostGIS set-based enrich → pure Go plan → apply (1 tx/pack)
```

Overpass API = dev/fallback adapter behind the same `FeatureSource` port
(no reproducible snapshot → no golden tests on it).

Do NOT parse .pbf in Go (multipolygon assembly = node-location index you
don't want to own) and do NOT use osm2pgsql (writes tables outside sqlc).

## 1. Sources and tag mapping (v1)

| kind | OSM tags | Notes |
|---|---|---|
| `park` | `leisure=park` | Exclude `leisure=garden`. NL has many pocket parks → area/evidence threshold required. |
| `beach` | `natural=beach` | Keep nudism/access/supervised in attrs. Exclude `nudism=yes/designated` by default. |
| `site` | `tourism=picnic_site`, `tourism=camp_site` | Camp sites often paid/bookable → keep `fee`, `reservation` in attrs. |
| `table` | `leisure=picnic_table` | Mostly **evidence, not spots** (see clustering). |

Other features carrying `picnic_table=yes` count as evidence for the
parent spot.

**Amenities** (same pass, separate function reading raw layer):

| amenity type | OSM tags |
|---|---|
| toilet | `amenity=toilets` |
| water | `amenity=drinking_water` |
| bbq | `amenity=bbq`, `leisure=firepit` |
| playground | `leisure=playground` |
| dog_area | `leisure=dog_park` |
| shelter | `amenity=shelter` |
| parking | `amenity=parking` (not private/customers) |
| bicycle_parking | `amenity=bicycle_parking` (relevant in NL) |

Skip benches (noisy; table already covers seating).

**Hard excludes**: `access=private/no`, military, gardens. Excluded rows
stay in the raw layer for audit.

**Other sources**:

- Municipal/national open data — cross-check parks (verify licences).
- Wikidata via `wikidata=*` tag → names + fame signal.
- User `missing` reports = legitimate ingest channel (`source='user'`,
  enters as `pending`).
- Skip Google/Foursquare — licence restricts storing data.

**Licensing**: OSM is **ODbL** — attribution + share-alike matter, and
the MCP server exposes data publicly. Keep OSM-derived rows separable
via provenance (`t_spot_sources.source`) and get a legal read.

## 2. Pipeline (as implemented)

```
ingest.extract.refresh → Geofabrik .pbf download (per osm_extract),
                         .prev rotation, atomic tmp+rename
ingest.osm             → per pack, one t_job_runs row + one tx:
  [outside tx] OsmiumSource.Open:
    resolve <extract_dir>/<basename(pack.osm_extract)>.osm.pbf
    (OSMIUM_PBF overrides for single-file dev)
    osmium tags-filter  → cached filtered.osm.pbf   (per extract+filter hash)
    osmium export -f geojsonseq -u type_id          (cached .geojsonseq)
    artifacts invalidated when input pbf mtime > artifact mtime
  [inside tx] Go stream Features():
    geojsonseq lines (RS 0x1E prefix) → parse → bbox+margin prefilter
    → Classify (pure) → InsertRaw inline (no row buffer)
    undecodable lines counted → stats.excluded_by_reason["parse_error"]
  Planner (pure Go, in-memory inputs loaded via sqlc):
    absorb children (via='absorbed'), orphan table DBSCAN clusters,
    match/create/update/link/close, fallback spatial+name matching,
    cross-pack ownership skip (via='primary' owned by another pack)
  Apply → guard checks → stats → pg_notify('pickhour_ingest','') → commit
```

Worker LISTENs on `pickhour_ingest`, debounces 15s, then triggers
`scoring.recompute` under its own advisory lock — ingest completion wakes
scoring without NATS or hard job ordering.

Cadence: `ingest.osm` daily, **no-ops when the extract is not newer than
the pack's last import** (freshness gate) → failed runs self-heal.
`ingest.extract.refresh` weekly.

**Disk model (40 GB VPS target)**: raw `.osm.pbf` files are deleted after
each `ingest.osm` run (`EnsureExtracts` backfills only missing ones up
front; `RemoveExtract` cleans up post-run). Steady-state disk = osmium
artifacts only (filtered pbf + geojsonseq ≈ 20% of raw size), so the
worker holds ~30-50 country extracts in ~10-15 GB instead of ~60 GB.
Trade-off: a filter-expression change or artifact loss triggers one
re-download of that extract (~1-4 GB) via `EnsureExtracts`.

## 3. Clustering (as implemented)

Hierarchy: **park ⊃ picnic_site ⊃ table**.

- **Containers first**: parks, beaches, camp sites, picnic-site areas are
  candidate spots.
- **Absorb children**: tables/picnic sites inside a park/beach polygon
  become *amenities/evidence* of that spot — `t_spot_sources.via =
  'absorbed'`. Absorbed sources do **not** keep a spot alive when the
  primary geometry vanishes (only `via='primary'` links count for
  sourceless cleanup).
- **Anchor point**: spot `anchor` = `ST_PointOnSurface` of the area;
  `area` holds the full polygon. API distance queries use `area`.
- **Orphan tables**: unabsorbed `kind=table` candidates are clustered in
  Postgres via `ST_ClusterDBSCAN` (eps 80 m, min 3 tables) → each cluster
  becomes a synthetic `kind='site'` spot with every member table attached
  as a `primary` source. Quality = `TableEvidence(25) + 10×size` (cap
  100). Isolated tables (< min cluster) never become spots.
- **Boundary straddlers**: features filtered by geometry∩(pack bbox +
  0.01° margin); `(source, source_id)` is globally unique and claimed by
  the **first pack that ingests it** (first-owner semantics) — later packs
  count `foreign_owned` and skip, so no duplicate spots and no provenance
  theft. API serves by bbox so straddlers remain visible.
- **Geometry hygiene**: `ST_Multi`, MultiPolygon `area` column; osmium
  assembles multipolygons (Overpass path falls back to centroid).

## 4. Diff and state machine (as implemented)

**Identity**: match by `(source, source_id)` — format `n<w>`, `w<id>`,
`r<id>` identical across Overpass and osmium. On miss, **fallback
matching**: same kind + normalized name + centroid within 30 m, unique
candidate only → re-links source instead of duplicating (`fallback_matched`
stat). Re-link, don't recreate.

**Change detection**: `t_spot_sources.content_hash` = sha256 over
kind + name + sorted tags + normalized geometry. Needed because osmium
exports carry `Version=0` — version-only detection could never reopen.
`SourceChanged` compares hash; absorbed children pass empty hash.

**Disappearance**: per-source `missing_runs`; spot closes when all
`primary` sources missing ≥ `CloseAfterMissingRuns` (2). Sourceless
spots (all primary sources moved via fallback/foreign ownership) close
with `status_reason='sources_moved'`.

**Promotion** — quality 0–100 from facts (name, area, table evidence,
amenity variety, access tags):

- ≥ `AutoActivate` (60) → `active`
- `KeepPending` (35)..AutoActivate → `pending` (admin review)
- < KeepPending → raw layer only, never a spot

**Override rules**:

- Importer never moves `hidden`, `merged`, `closed_by_report` spots.
- `closed_by_report` set by admin moderation of an accepted `closed`
  report (`CloseSpotForReport`, active/pending → closed); ingest never
  auto-reopens it.
- Import-closed spots reopen when source content hash changes.
- API serves only `active` spots in `beta`/`live` packs.

## 5. Jobs and schedule

| Job | Interval | What |
|---|---|---|
| `ingest.extract.refresh` | weekly | Conditional download per distinct region, keep last 2 files |
| `ingest.osm` | daily | No-op unless extract newer than pack's last import; iterates packs concurrency 1, one `t_job_runs` row + one tx per pack |

- No hard ordering with `scoring.recompute` — downstream discovers work
  from table state.
- Failure semantics: heavy stage outside tx, nothing persists on
  failure, no inner retries (next tick retries).
- CLI: `worker -job ingest.osm -pack <slug> [-dry-run]` — dry-run runs
  the full tx and rolls back, printing stats.
- Observability: `t_job_runs.stats` jsonb per pack run — `features`,
  `candidates`, `created`, `updated`, `activated`, `pending`, `closed`,
  `absorbed`, `amenities`, `fallback_matched`, `foreign_owned`,
  `orphan_sites`, `excluded_by_reason` (incl. `parse_error`),
  `guard_tripped`. On successful commit the tx emits
  `pg_notify('pickhour_ingest','')`; the worker listener debounces 15 s
  and runs `scoring.recompute`.

## 6. Schema (migrations 000003 + 000004, as implemented)

| Table | Change |
|---|---|
| `t_ingest_features` (new) | `(pack_id, source, source_id)` PK, role, version, tags jsonb, geometry, content_hash, `run_id → t_job_runs`, GiST |
| `t_spot_sources` | `+ last_seen_at`, `+ missing_runs`, `+ via ('primary'/'absorbed')`, `+ content_hash` |
| `t_spots` | `+ quality`, `+ attrs jsonb`, `+ names jsonb`, `+ curated`, `+ status_reason`, `+ status_changed_at`, `+ anchor`, `+ area` |
| `t_spot_amenities` | `+ count` |
| `t_job_runs` | `+ stats jsonb` |
| `t_city_packs` | `+ osm_extract`, `+ extract_fetched_at` |

## 7. Code map (replaces the original sketch)

- `internal/modules/ingest/service.go` — `Service.RunPack`, planner,
  `Stats`, `Evidence`/`Policy.NextStatus` (pure, unit-tested).
- `internal/modules/ingest/osm/classify.go` — tag → role/kind/amenity,
  quality, excludes (pure).
- `internal/modules/ingest/osm/osmium.go` — `OsmiumSource`: artifact
  cache, per-extract pbf resolution, geojsonseq streaming, bbox filter.
- `internal/modules/ingest/osm/overpass.go` — dev `FeatureSource`.
- `internal/modules/ingest/extract.go` — Geofabrik downloader.
- `db/queries/ingest.sql` — all raw/planner/cluster/sourceless SQL.
- `internal/infrastructure/persistence/ingest_repo.go` — tx-scoped Store.
- `cmd/worker/main.go` — job registry, one-shot flags, LISTEN/NOTIFY
  listener.
- Admin: `db/queries/admin.sql` + `internal/modules/admin/` — accepted
  `closed` reports → `CloseSpotForReport`.

## 8. Tests

- Classifier: pure unit tests on tag maps.
- Planner golden (DB-gated, `DATABASE_URL`): park absorbs children ·
  orphan cluster → site spot · idempotent rerun · below-floor excluded ·
  missing-source close after 2 runs. All passing.
- Verified end-to-end on real extracts (see §10).
- Not yet done: hand-cut `.osm.pbf` mini-extract golden, two-version diff
  fixture, ~20 known-places tuning checklist.

## 9. Decisions (resolved defaults)

| # | Decision | Resolution |
|---|---|---|
| D1 | Tables as spots? | Orphan clusters (≥3 within 80 m) → synthetic `site`; absorbed inside parks; isolated ignored |
| D2 | Camp sites in v1? | Yes — `tourism=camp_site` → `kind=site`, fee/reservation kept in attrs |
| D3 | Auto-activate | `AutoActivate=60`, `KeepPending=35`; pending visible to admin review |
| D4 | `curated` lock | Yes — importer never mutates curated/hidden/merged |
| D5 | Close policy | 2 missing runs; blast-radius guard aborts run; `closed_by_report` is admin-only |
| D6 | Shade input | Deferred — scoring `s_shade` review pending |
| D7 | osmium-tool | In worker image via `OSMIUM_CMD` wrapper; prod should bake `osmium-tool` into image |
| D8 | Packs: country extract + clip | Yes — per-`osm_extract` pbf, per-pack bbox stream filter |

## 10. Implementation status & verified results (2026-11-12)

Verified through the real osmium path (Geofabrik pbf → tags-filter →
geojsonseq → classify → tx → stats):

| Extract | Pack | Features | Created | Updated | Activated | Notes |
|---|---|---|---|---|---|---|
| luxembourg-latest (46 MB) | luxembourg | 30,322 | 249→329* | — | — | idempotent rerun `created=0` |
| belgium-latest (666 MB) | brussels | 24,960 | 136 | — | 42 | artifacts ~14 MB filtered / 124 MB export |
| netherlands-latest (1.4 GB) | amsterdam-test | 53,615 | 393 | 244 | 149 | 24,682 amenity links |
| 〃 | rotterdam | 28,380 | 55 | 263 | 27 | 22,180 amenities |
| 〃 | utrecht | 22,216 | 58 | 192 | 40 | 18,513 amenities |
| 〃 | denhaag | 38,567 | 68 | 361 | 34 | 32,857 amenities |

\* second run added orphan-cluster sites; `t_spot_sources` now shows
`via`: 3,108 primary / 407 absorbed across Randstad packs.

**Known gaps / accepted limitations** (for reviewers):

- `InsertRaw` is per-row INSERT — fine at pack scale (~50k features),
  will need COPY/batching for country-sized packs.
- Feature collection is streamed (no row buffer) but per-row insert
  keeps the tx open for the whole stream — long tx on huge extracts.
- First-owner cross-pack semantics: a straddler belongs to whichever
  pack ingests first; no pack-scoped `t_spot_sources` yet. Acceptable
  while packs are city-sized and non-overlapping in practice.
- `Evidence.MissingRuns`/`AllSourcesMissing`/`RecentConfirm` fields are
  populated but the confirm-shield branch is not wired (D5 simplified).
- `osmium extract` (boundary clip) unused — bbox filter happens in Go;
  revisit if a single pack approaches country scale (memory of
  geojsonseq stream is fine, but per-pack re-stream of a huge export
  file is wasteful).
- Raw `.osm.pbf` deleted post-ingest (disk hygiene) — a same-week
  re-ingest reuses artifacts; only filter changes or artifact loss
  force a re-download via `EnsureExtracts`.
- Overpass path: relations → centroid only; no golden fixtures on it.
- Production worker image does not yet ship `osmium-tool` (dev runs it
  via `pickhour-osmium` docker image + `OSMIUM_CMD`).
- `ingest.extract.refresh` download path implemented but not exercised
  end-to-end on a schedule yet.
- Amenity `count` column exists; scoring consumption of amenities/
  shade deltas (`s_amenity`, `s_shade`) not yet reviewed.
- Country-scale ops docs (extract volume sizing, rotation, cache
  cleanup, monitoring) not yet written.
