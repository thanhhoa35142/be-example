-- Getaways threshold check (docs/GETAWAYS_SPEC.md §3.2). Read-only. Edit the date range and cap values (t_scoring_configs.thresholds) before running.
-- docker exec -i picnic-postgres psql -U picnic -d picnic -At < scripts/analysis/getaways_thresholds.sql
-- city-hour weather score = median over active spots of LEAST(s_weather, cap)
with caps as (select 'rain' r, 40 v union all select 'heat',30 union all select 'cold',30 union all select 'dark',10 union all select 'twilight',40),
ch as (
  select p.slug, (ss.ts_hour at time zone p.tz)::date d, ss.ts_hour,
    percentile_cont(0.5) within group (order by least(ss.s_weather, coalesce(c.v,100))) w
  from t_spot_scores ss join t_spots s on s.id=ss.spot_id and s.status='active'
  join t_city_packs p on p.id=s.city_pack_id and p.status in ('beta','live')
  left join caps c on c.r=ss.cap_reason
  where ss.s_weather is not null and ss.ts_hour >= '2026-11-16' and ss.ts_hour < '2026-11-23'
    and extract(hour from ss.ts_hour at time zone p.tz) between 8 and 19
  group by 1,2,3)

, x as (select slug,d,ts_hour,w, lag(w) over (partition by slug,d order by ts_hour) pw,
      lag(ts_hour) over (partition by slug,d order by ts_hour) pt from ch)
select t, count(distinct slug) filter (where w>=t and pw>=t and ts_hour-pt='1 hour') cities,
  count(distinct (slug,d)) filter (where w>=t and pw>=t and ts_hour-pt='1 hour') citydays,
  (select count(distinct (slug,d)) from ch) total_cd
from x, (values (50),(60),(65),(70),(75),(80),(85)) th(t) group by t order by t;