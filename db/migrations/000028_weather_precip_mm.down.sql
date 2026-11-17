ALTER TABLE t_weather_hourly_forecasts
    DROP CONSTRAINT IF EXISTS ck_t_weather_hourly_precip_mm,
    DROP COLUMN IF EXISTS precip_mm;
