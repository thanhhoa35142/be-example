// Package reports handles user-submitted spot reports, community crowd
// reports, and score feedback. Anti-abuse: one spot report per device/spot/
// type/day (DB constraint); spot reports, crowd reports and score feedback are
// each rate-limited per device over a rolling 24h.
package reports
