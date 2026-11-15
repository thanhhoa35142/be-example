# Schema backlog — deferred structural changes

These were considered during the 0001 baseline rewrite but intentionally NOT
applied yet. Each needs product/code sign-off before migration.

| Item | Proposal | Why deferred |
|---|---|---|
| `t_device_subscriptions` | Merge into `t_devices` (one row per device+txn is thin) | Touches entitlement link/unlink flow; needs code change first |
| `t_plan_limits` | Move limits into code config | Table is 2 rows; DB lookup adds a round-trip for no benefit yet |
| `t_api_key_grants` | Replace with `scopes text[]` on `t_api_keys` | Grants are few; array is simpler — but wildcard/deny semantics need design |
| `t_alert_rules` `h3_cell`/`radius_m` | Remove geo-area alerting | Product decision: alerts are per-spot or per-H3-area? Keep until decided |
| `t_score_feedbacks` | Merge into `t_events` (name=`score_feedback`) | Loses typed columns/unique slot; events are schemaless |
| `t_spots` `country`/`tz`/`city` | Drop repeated denormalized columns | They duplicate `t_city_packs`; but hot read path avoids a join — measure first |
| `t_spot_scores` `reasons` | Drop JSONB reasons column | Server sends reason codes; check if app actually consumes per-hour reasons |
