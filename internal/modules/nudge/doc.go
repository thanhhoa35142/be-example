// Package nudge implements the good-day push (docs/GOOD_DAY_PUSH_SPEC.md): one
// passive APNs push to a user who has gone quiet when a day near their home
// area looks unusually good.
//
// The home area is an H3 res-7 cell derived from a coordinate the server
// discards immediately; only the cell is stored. The package owns the
// subscription use cases (opt in/out) and the periodic Run that picks the day
// and spot per cell, claims one delivery per device and local day, and hands
// the send to a Sender.
package nudge
