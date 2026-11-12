-- Seed crowd_curve config_version 1: hourly factors per spot kind.
-- Park-family (park/site/table) peaks weekday lunch + weekend afternoon;
-- beach peaks midday. Values are starting assumptions (spec §16).
INSERT INTO crowd_curve (kind, dow, hour, factor, config_version)
SELECT k.kind, d.dow, h.hour,
    CASE
        WHEN k.kind = 'beach' THEN CASE
            WHEN h.hour BETWEEN 0 AND 7 THEN 0.10
            WHEN h.hour BETWEEN 8 AND 10 THEN CASE WHEN d.dow IN (0, 6) THEN 0.60 ELSE 0.45 END
            WHEN h.hour BETWEEN 11 AND 15 THEN CASE WHEN d.dow IN (0, 6) THEN 1.15 ELSE 1.00 END
            WHEN h.hour BETWEEN 16 AND 18 THEN CASE WHEN d.dow IN (0, 6) THEN 0.95 ELSE 0.80 END
            WHEN h.hour BETWEEN 19 AND 21 THEN 0.40
            ELSE 0.15
        END
        ELSE CASE
            WHEN h.hour BETWEEN 0 AND 5 THEN 0.15
            WHEN h.hour BETWEEN 6 AND 8 THEN 0.35
            WHEN h.hour BETWEEN 9 AND 11 THEN CASE WHEN d.dow IN (0, 6) THEN 0.65 ELSE 0.45 END
            WHEN h.hour BETWEEN 12 AND 13 THEN CASE WHEN d.dow IN (0, 6) THEN 0.95 ELSE 0.85 END
            WHEN h.hour BETWEEN 14 AND 17 THEN CASE WHEN d.dow IN (0, 6) THEN 1.00 ELSE 0.70 END
            WHEN h.hour BETWEEN 18 AND 20 THEN 0.55
            ELSE 0.25
        END
    END,
    1
FROM (VALUES ('park'), ('beach'), ('site'), ('table')) AS k(kind)
CROSS JOIN generate_series(0, 6) AS d(dow)
CROSS JOIN generate_series(0, 23) AS h(hour)
ON CONFLICT DO NOTHING;
