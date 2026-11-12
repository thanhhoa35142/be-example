// Package scoring computes spot_score rows ahead of time: weighted components
// (weather 40%, crowd 25%, amenities 20%, shade 10%, freshness 5%), hard caps
// for rain/extreme heat, versioned scoring_config weights, reason codes with
// parameters, and a 1–3 confidence level. It only writes changed rows.
package scoring
