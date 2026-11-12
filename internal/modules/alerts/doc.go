// Package alerts owns alert rules ("good window" notifications) and delivery
// de-duplication via alert_delivery. Alert areas are stored coarsely
// (H3 cell or ~1 km rounding) for privacy.
package alerts
