-- Hourly precipitation amount (mm) for the wet-grass estimate (D1 of
-- FEATURE_PLAN_DECISION_PLANS_GRASS.md). Nullable: providers that cannot
-- supply it leave it NULL, and rows written before this migration stay NULL.
-- `real` like the other measured columns (temp, wind, uv).
ALTER TABLE t_weather_hourly_forecasts
    ADD COLUMN precip_mm real,
    ADD CONSTRAINT ck_t_weather_hourly_precip_mm CHECK (precip_mm IS NULL OR precip_mm >= 0);
