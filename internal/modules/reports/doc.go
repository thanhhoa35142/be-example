// Package reports handles user-submitted spot reports, community crowd
// reports, and score feedback. Anti-abuse: one spot report per device/spot/
// type/day (DB constraint), crowd reports rate-limited per device.
package reports
