-- Perf seed: one dense synthetic pack ("perf-amsterdam") with :spots spots
-- on a deterministic grid + amenities + 48h of hourly scores.
-- Idempotent: wipes previous perf-* packs first.
-- Run:  psql -v spots=3000 -f seed.sql   (or stdin via compose exec)

\set ON_ERROR_STOP on
BEGIN;

DELETE FROM t_spots
 WHERE city_pack_id IN (SELECT id FROM t_city_packs WHERE slug LIKE 'perf-%');
DELETE FROM t_city_packs WHERE slug LIKE 'perf-%';

INSERT INTO t_city_packs (slug, name, country, tz, boundary, status, last_import_at)
VALUES (
  'perf-amsterdam', 'Perf Amsterdam', 'NL', 'Europe/Amsterdam',
  ST_Multi(ST_MakeEnvelope(4.65, 52.28, 5.15, 52.50, 4326)),
  'live', now()
);

-- Grid over the Amsterdam area: ~60 cols, deterministic pseudo-random
-- jitter so bbox queries return varied subsets. :spots must be <= ~20000.
INSERT INTO t_spots
  (city_pack_id, kind, name, geom, country, city, tz, cell_id, status, last_verified_at)
SELECT p.id,
       (ARRAY['park','site','beach','table'])[(i % 4) + 1],
       'Perf spot ' || i,
       ST_SetSRID(ST_MakePoint(
         4.70 + (i % 60) * 0.006 + ((i * 37) % 100) / 100000.0,
         52.30 + (i / 60)  * 0.003 + ((i * 91) % 100) / 200000.0
       ), 4326)::geography,
       'NL', 'Amsterdam', 'Europe/Amsterdam',
       600000 + i,
       'active', now() - (i % 72 || ' hours')::interval
FROM t_city_packs p
CROSS JOIN LATERAL generate_series(1, :spots) AS i
WHERE p.slug = 'perf-amsterdam';

-- Amenities on a deterministic subset (keeps the AND-filter subquery real).
INSERT INTO t_spot_amenities (spot_id, type, distance_m, source)
SELECT s.id, a.type, a.d, 'perf-seed'
FROM t_spots s
JOIN t_city_packs p ON p.id = s.city_pack_id AND p.slug = 'perf-amsterdam'
JOIN LATERAL (VALUES
  ('toilet',     40 + (abs(hashtext(s.id::text)) % 200)::int),
  ('playground', CASE WHEN abs(hashtext(s.id::text || 'pg')) % 3 = 0
                      THEN 60 + (abs(hashtext(s.id::text || 'pg')) % 300)::int END),
  ('water',      CASE WHEN abs(hashtext(s.id::text || 'w')) % 4 = 0
                      THEN 30 + (abs(hashtext(s.id::text || 'w')) % 150)::int END)
) AS a(type, d) ON a.d IS NOT NULL;

-- 48 hourly score rows per spot starting at the current hour.
INSERT INTO t_spot_scores
  (spot_id, ts_hour, config_version, score, s_weather, s_crowd, s_amenity,
   s_shade, s_fresh, cap_reason, reasons, confidence, computed_at)
SELECT s.id,
       date_trunc('hour', now()) + (h || ' hours')::interval,
       (SELECT max(version) FROM t_scoring_configs),
       40 + ((abs(hashtext(s.id::text)) + h * 7) % 55),
       70, 60, 80, NULL, 90,
       CASE WHEN h % 11 = 0 THEN 'heat' END,
       '[]'::jsonb,
       2,
       now()
FROM t_spots s
JOIN t_city_packs p ON p.id = s.city_pack_id AND p.slug = 'perf-amsterdam'
CROSS JOIN LATERAL generate_series(0, 47) AS h;

COMMIT;

SELECT p.slug, count(s.id) AS spots,
       (SELECT count(*) FROM t_spot_scores sc
         JOIN t_spots s2 ON s2.id = sc.spot_id
         JOIN t_city_packs p2 ON p2.id = s2.city_pack_id
        WHERE p2.slug = 'perf-amsterdam') AS score_rows
FROM t_city_packs p
LEFT JOIN t_spots s ON s.city_pack_id = p.id
WHERE p.slug LIKE 'perf-%'
GROUP BY p.slug;
