// Package digest owns the weekly weekend digest (Plus only): a Thursday-evening
// push naming the single best picnic window of the coming weekend across the
// spots a device chose. Preferences live in t_weekly_digests; the worker job
// `digest.weekly` ticks hourly and sends to devices whose local clock matches
// the digest's send_dow/send_hour, at most once per local date.
package digest
