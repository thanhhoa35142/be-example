# Ingest data strategy — spots from OSM (and beyond)

Status: **proposed architecture** (brainstormed, not yet fully implemented).
Scope: how spot data (parks, beaches, picnic sites, picnic tables) enters
`t_spots` and stays fresh. This doc is the source of truth for the ingest
module; update it when decisions change.

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

## 2. Pipeline

```
ingest.extract.refresh → conditional download of region .pbf
                         (shared by packs with same osm_extract; keep last 2)
ingest.osm             → per pack, concurrency 1 (osmium memory):
  1. osmium extract (pack boundary) → tags-filter → export geojsonseq  [outside tx]
  2. Go: stream → Classify (pure) → COPY into raw layer                [tx begins]
  3. PostGIS (sqlc, set-based): make-valid, area, anchor point,
     containment, table clusters, duplicate overlaps, amenity distances/counts
  4. Go: Plan (pure): match existing spots → create/update/link/close/reopen
  5. Guard checks, Apply, set last_import_at, write stats              [tx commits]
```

Cadence: Geofabrik updates daily; weekly fresh downloads are enough for
country-sized packs. `ingest.osm` runs daily but **no-ops unless the
extract is newer than the pack's last import** → failed runs self-heal.

Amenities: same pass logically but a **separate function** reading the
raw layer → re-runnable after changing radius rules without re-download.

Nominatim not needed: country/tz come from the pack; unnamed spots get a
read-time label from `place=suburb/neighbourhood` polygons extracted in
the same pass.

**Gotchas**:

- pgx binary COPY probably can't encode PostGIS geometry → COPY GeoJSON
  as text, convert with `ST_GeomFromGeoJSON` in INSERT…SELECT.
- osmium flag names (`-p` polygon, `--unique-id`) from memory — verify
  against installed version.

## 3. Clustering

Hierarchy: **park ⊃ picnic_site ⊃ table**.

- **Containers first**: parks, beaches, camp sites, picnic-site areas are
  candidate spots.
- **Absorb children**: tables/picnic sites inside a park polygon become
  *amenities/evidence* of that park, not spots. Their source rows attach
  to the same spot (`t_spot_sources` allows many sources per spot).
- **Anchor point**: park `geom` = centroid of its table cluster if one
  exists, else `ST_PointOnSurface(area)`. "Near me" queries measure
  distance to `area` when present, not the anchor.
- **Orphan tables**: cluster with `ST_ClusterDBSCAN` (eps ~40 m). A
  cluster of ≥ N tables → synthetic site, `source_id = cluster:<min id>`.
  A single isolated table is not a spot by default.
- **Duplicates**: same-name polygons touching/overlapping > ~50% merge
  before creation.
- **Boundary straddlers**: assign by `ST_PointOnSurface` inside pack
  boundary, keep whole polygon (don't clip).
- **Geometry hygiene**: `ST_MakeValid`, `ST_Multi`, light simplify,
  drop slivers.

## 4. Diff and state machine

**Identity**: match by `(source, source_id)`. OSM IDs change when mappers
redraw → for unmatched candidates fall back to spatial overlap + name
against spots whose sources all vanished. **Re-link, don't recreate.**

**Change detection**: content hash over kind, name, attrs and normalized
geometry — NOT just OSM version (a new table inside a park doesn't bump
the park's version; amenities recompute independently).

**Disappearance**: increment `missing_runs`. Close only when ALL sources
missing for ≥ 2 healthy runs AND no accepted confirm report in last 180
days (then log for review instead). **Blast-radius guard**: abort the run
if it would close > ~15% of a pack's active spots (truncated extract /
broken filter).

**Promotion** — quality 0–100 from facts (name, area, table count,
amenity variety, explicit public access):

- ≥ AutoActivate → `active`
- KeepPending..AutoActivate → `pending` (admin review)
- < KeepPending → stays in raw layer, never a spot

**Override rules**:

- Importer never moves `hidden` or `merged` spots (hidden = human "no";
  re-import hits same source_id, can't resurrect).
- Spot closed via accepted report reopens **only by admin** (OSM lags
  reality).
- Spot closed by import reopens when its source object changes after
  close (uses `closed→active` edge).
- API serves only `active` spots in `beta`/`live` packs.

## 5. Jobs and schedule

| Job | Interval | What |
|---|---|---|
| `ingest.extract.refresh` | weekly | Conditional download per distinct region, keep last 2 files |
| `ingest.osm` | daily | No-op unless extract newer than pack's last import; iterates packs concurrency 1, one `t_job_runs` row + one tx per pack |

- No hard ordering with `scoring.recompute` — downstream discovers work
  from table state.
- Failure semantics: heavy stage outside tx, nothing persists on
  failure, ~20 min timeout per pack, no inner retries (next tick
  retries), downloads via shared `httpx` policy.
- CLI: `worker ingest --pack <slug> [--dry-run]` — dry-run does the full
  tx and rolls back, printing the diff (for tuning thresholds).
- Observability: stats jsonb per run (features read, excluded by reason,
  created, updated, linked, closed, reopened, needs-review, guard
  tripped, per-stage durations). Alert when features_read drops >30%
  vs previous run.

## 6. Schema deltas (migration 000003)

| Table | Change |
|---|---|
| `t_ingest_features` (new) | `(pack_id, source, source_id)` PK, role, version, tags jsonb, geometry, content_hash, run_id, GiST |
| `t_spot_sources` | `+ last_seen_at`, `+ missing_runs smallint` |
| `t_spots` | `+ quality smallint`, `+ attrs jsonb`, `+ curated bool`, `+ status_reason text`, `+ status_changed_at` |
| `t_spot_amenities` | `+ count smallint` (PK allows one row/type → counts would be lost) |
| `t_job_runs` | `+ stats jsonb` |
| `t_city_packs` | `+ extract_fetched_at timestamptz` |

## 7. Go sketch (ports)

```go
type RawFeature struct {
    Source, SourceID string // "osm", "n123" | "w456" | "r789"
    Version          int
    Tags             map[string]string
    GeoJSON          []byte // Point | Polygon | MultiPolygon, 4326
}

type FeatureSource interface {           // osmium (prod), overpass (dev), fixture (tests)
    Open(ctx context.Context, p Pack) (Snapshot, error)
}
type Snapshot interface {
    AsOf() time.Time
    Features() iter.Seq2[RawFeature, error]
    Close() error
}

type Classifier interface{ Classify(RawFeature) Classified }  // pure, table-tested
type Classified struct {
    Role Role // Candidate | Amenity
    Kind SpotKind
    Amenity AmenityType
    Name, Attrs map[string]any
    Quality int    // from tags alone
    Exclude string // non-empty = reason; row kept for audit
}

type Store interface {                    // sqlc-backed, tx-scoped
    WithTx(ctx context.Context, fn func(Store) error) error
    ReplaceRaw(ctx, packID, asOf, rows) error
    LoadPlanInput(ctx, packID) (PlanInput, error) // spatial SQL
    Apply(ctx, p Plan) error
}

type Policy struct {
    AutoActivate, KeepPending int     // e.g. 60 / 35, tune on golden set
    CloseAfterMissingRuns     int     // 2
    MaxCloseFraction          float64 // 0.15
    ConfirmShieldDays         int     // 180
    ClusterEpsM               float64 // 40
    MinTablesForSite          int     // 3
}
```

`Policy.NextStatus(cur, evidence)` is pure — every state-machine edge is
unit-testable without a DB.

`RunPack`: `src.Open` (heavy I/O, outside tx) → `WithTx { ReplaceRaw →
LoadPlanInput → Plan → guard → Apply }`; dry-run rolls back and prints
the diff.

## 8. Golden tests

Two levels:

1. **Classifier fixtures**: tag maps → Classified, no DB.
2. **Mini-extract golden**: hand-cut `.osm.pbf` (few hundred KB) through
   the real pipeline on Postgres+PostGIS, compared to expected JSON.
   Two fixture versions for diff testing.

Cases: park+tables absorbed · isolated table (no spot) · site inside
park · duplicate polygons · multipolygon with hole · private park
excluded · nude beach excluded · remapped object relinked · vanished →
closed after 2 runs · vanished+recently-confirmed (not closed) ·
hidden stays hidden · truncated extract trips guard · park straddling
boundary. Plus ~20 known real places as manual tuning checklist.

## 9. Open decisions

| # | Decision | Default | Trade-off |
|---|---|---|---|
| D1 | Tables as spots? | Absorb; isolated single tables are not spots | Fewer/better spots vs missing lone tables |
| D2 | Camp sites in v1? | Yes, fee/reservation in attrs | Product fit vs mostly paid sites |
| D3 | Auto-activate vs manual | Auto above threshold; manual review for first pack | Speed vs garbage-score risk |
| D4 | `curated` lock on admin-edited spots | Yes | Protects edits vs extra columns |
| D5 | Close policy | 2 runs, 180d confirm shield, 15% guard | Fewer false closures vs staler data |
| D6 | Shade input | Start without trees | `natural=tree` counts may be huge; check s_shade first |
| D7 | osmium-tool in worker image | Yes | Bigger image vs far less geometry code |
| D8 | Packs: country extract + clip | Yes | Larger download vs deterministic/reproducible |

## Known weak spots (unverified)

- OSM tag semantics + Geofabrik cadence — verified from sources.
- Everything else from memory: osmium flags, pgx geometry COPY behavior,
  Overpass fair-use, ODbL obligations, threshold numbers (60/35, 40 m,
  15%, 2 runs, 180 d) — starting guesses to tune on the golden set.
- `s_amenity`/`s_shade` consumption in scoring not yet reviewed — could
  change amenity `count` and shade deltas.

## Implementation status (2026-11-12)

- `ingest.Service.RunPack` + tx-scoped Store: implemented, verified idempotent
  on Overpass (Amsterdam/RTM/Utrecht/Den Haag) and osmium (Andorra).
- `osm.OverpassSource`: dev path, relations → centroid (v1 limitation).
- `osm.OsmiumSource`: production path — `osmium tags-filter` (cached per
  extract) + `osmium export -f geojsonseq -u type_id`; multipolygons
  assembled by osmium; pack-bounds filtering in Go. Runs through a
  configurable command prefix (`OSMIUM_CMD`) so it works inside docker.
- `ingest.extract.refresh`: Geofabrik .pbf download + `.prev` rotation.
- Worker: `ingest.osm`, `ingest.extract.refresh` jobs +
  `-job/-pack/-dry-run` one-shot flags.
- Known gaps: no spatial+name fallback matching, no orphan table
  clustering, `run_id` NULL on raw rows, LineString candidates keep
  point anchor only, `osmium extract` step not used yet (bbox filter
  happens in Go — revisit for country-sized extracts).
