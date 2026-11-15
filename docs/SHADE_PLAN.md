# Shade / sun by hour — Plan (D8)

*Status: planned, 2026-11-13. Tier: **Plus**. Roadmap item 1.4 (`pickhour-app/docs/ROADMAP.md`).*

Goal: for each spot and hour, say whether people will sit in **shade, partial shade or full sun**, and use it in the score. Scoring already has a `shade` component (10% weight, currently absent → weights renormalize, see `API.md`); this plan fills it.

## 1. Method

1. **Sun position** — NOAA/SPA formulas (error < 0.01°, not a source of error).
2. **Shadow casters** — a surface model (buildings + trees + terrain) around each spot, from the best open source for that location:

| Tier | Source | Coverage | Notes |
|---|---|---|---|
| A | Open LiDAR DSM 0.5–1 m | NL (AHN), England (EA), DK, FI, some DE states, many US cities (USGS 3DEP), some AU cities | Best; includes trees and buildings |
| B | Meta/WRI Global Canopy Height 1 m (CC-BY 4.0) + OSM/Overture buildings + Copernicus DEM 30 m | Global | Canopy MAE ≈ 3 m, imagery ~2018–2020, tall trees underestimated; building heights often missing → estimate from `building:levels` or default |

3. **Precompute, static** — shadows depend only on date, hour and geometry, not weather. Per spot: sample points (picnic table = 1 point; park/beach = grid inside the polygon), compute shaded fraction **per hour × every 2 weeks of the year**. Runs offline next to ingest (cloud workflow), stored as one compact array per spot. Recompute only when a spot's geometry changes or a new source tier is added.
4. **At request time** — combine the precomputed shaded fraction with weather:
   - clear sky + hot / high UV → shade raises `s_shade`;
   - cold → sun raises it;
   - overcast → shade is neutral.
5. **Season** — deciduous trees: apply a leaf-off factor in winter (canopy data is leaf-on); tree crowns let ~10–30% of light through even leaf-on.

## 2. What the user sees

- Three levels per hour: **Shady / Partly shady / Full sun**, plus UV index. No exact percentages (absorbs ±15% error).
- Tier B locations are labelled "estimated" (reason code, app translates).
- Feedback button "Was it shady?" next to the shade line → stored like score feedback, used for calibration.
- Motion M4 "sun path" in Spot Detail (UI_SPEC §8.1).
- Free sees the shade level only for today as a teaser? — **decide at build time**; default: Plus only.

## 3. Expected accuracy (estimate — not yet measured)

| Question | Tier A | Tier B |
|---|---|---|
| Is this picnic table shaded at 14:00? | ~85–90% correct | ~70–80% correct |
| % of park area in shade | ±5–10 pts | ±10–20 pts |

Main error sources: leaf-off season, porous crowns, outdated data (trees cut, new buildings). Beaches are trivial (≈ no shade).

## 4. Benchmark before committing (≈ 2 days)

Amsterdam, where AHN gives ground truth:

1. ~300 Amsterdam spots, compute shade with **Tier B only**.
2. Same spots with **Tier A (AHN)** as reference.
3. Compare per hour, summer and winter dates → agreement %, confusion between the 3 levels.

**Decision rule:**
- Tier B agreement ≥ ~70% → ship everywhere, Tier B labelled "estimated".
- Below that → ship only where Tier A exists; add countries as LiDAR is loaded.

Report results in this file (§6).

## 5. Work breakdown

| # | Task | Part | Days |
|---|---|---|---|
| 1 | Benchmark Amsterdam (§4) | BE | 2 |
| 2 | Precompute job + storage (migration + sqlc), source tier per spot | BE | 2 |
| 3 | `s_shade` in scoring + reason codes, `shade` level in API (`scores[]`, forecast) | BE | 1 |
| 4 | App: shade line in hourly chart, "Why 82?" row, sun path motion, feedback | App | 2 |
| 5 | Load Tier A per country | BE/Ops | 0.5–1 / country |

Total ≈ 6–8 days + Tier A per country.

## 6. Results

*(fill in after the benchmark)*
