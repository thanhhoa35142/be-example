package osm

// Status mirrors t_spots.status values relevant to ingest decisions.
type Status string

const (
	StatusPending Status = "pending"
	StatusActive  Status = "active"
	StatusClosed  Status = "closed"
	StatusHidden  Status = "hidden"
	StatusMerged  Status = "merged"
)

// Policy holds the tunable thresholds from INGEST_STRATEGY §4/§9.
type Policy struct {
	AutoActivate      int     // quality >= this → active
	KeepPending       int     // quality >= this → pending spot; below → raw only
	CloseAfterMissing int     // missing_runs needed before close
	MaxCloseFraction  float64 // abort run if closes exceed this share of active
	ConfirmShieldDays int     // recent accepted confirm shields from close
	MinTablesForSite  int     // orphan table cluster size that becomes a site
	// OrphanClusterEpsM is the DBSCAN epsilon in meters: tables within this
	// distance chain into one cluster.
	OrphanClusterEpsM float64
}

// DefaultPolicy is the starting point; tune on the golden set.
var DefaultPolicy = Policy{
	AutoActivate:      60,
	KeepPending:       35,
	CloseAfterMissing: 2,
	MaxCloseFraction:  0.15,
	ConfirmShieldDays: 180,
	MinTablesForSite:  3,
	OrphanClusterEpsM: 80,
}

// Evidence is what the planner knows about an existing spot at plan time.
type Evidence struct {
	CurrentStatus     Status
	Quality           int
	Trusted           bool // source is trusted (e.g. osm extract, not user)
	MissingRuns       int
	AllSourcesMissing bool
	RecentConfirm     bool // accepted confirm report within ConfirmShieldDays
	SourceChanged     bool // content hash changed since last import
	ClosedByReport    bool // closed via accepted user report → admin only
	Curated           bool // admin-edited; importer never touches
	// StatusReason is the spot's current t_spots.status_reason. Hidden and
	// closed spots are only re-evaluated when the importer itself put them
	// there (see ImporterHiddenReason / ImporterClosedReason) — admin
	// decisions stay final.
	StatusReason string
	// SourcePresent: the spot's primary source is in this run's snapshot.
	SourcePresent bool
}

// Status reasons the importer writes (t_spots.status_reason).
const (
	ReasonAutoQuality     = "auto_quality"
	ReasonAwaitingReview  = "awaiting_review"
	ReasonBelowFloor      = "below_quality_floor"
	ReasonVanished        = "vanished_before_review"
	ReasonSourcesMoved    = "sources_moved"
	ReasonSourceMissing   = "source_missing"
	ReasonSourceReturned  = "source_returned"
	ReasonClosedByReport  = "closed_by_report"
	ReasonDuplicateMerged = "duplicate_osm_area"
)

// ImporterHiddenReason reports whether a hidden spot was hidden by the
// importer (re-evaluable) rather than by an admin (final).
func ImporterHiddenReason(r string) bool {
	switch r {
	case ReasonBelowFloor, ReasonVanished, ReasonSourcesMoved:
		return true
	}
	return false
}

// ImporterClosedReason reports whether a closed spot was closed by the
// importer because its source vanished (reopens when the source returns).
func ImporterClosedReason(r string) bool {
	return r == ReasonSourceMissing || r == ReasonSourcesMoved
}

// NextStatus is the pure state-machine decision for one spot (INGEST_STRATEGY §4).
// Returns the new status and a machine-readable reason for t_spots.status_reason.
func (p Policy) NextStatus(e Evidence) (Status, string) {
	// Human/terminal states the importer must never move.
	if e.Curated {
		return e.CurrentStatus, "curated"
	}
	switch e.CurrentStatus {
	case StatusMerged:
		return e.CurrentStatus, ""
	case StatusHidden:
		// Admin-hidden is final; importer-hidden is re-scored like a new
		// candidate (quality may have recovered, or the source came back).
		if !ImporterHiddenReason(e.StatusReason) || !e.SourcePresent {
			return e.CurrentStatus, ""
		}
		if st, reason := p.NewSpotStatus(e); st != "" {
			return st, reason
		}
		return StatusHidden, e.StatusReason
	case StatusClosed:
		if e.ClosedByReport {
			return StatusClosed, ReasonClosedByReport // reopen is admin-only
		}
		// Importer closed it because the source vanished: reopen as soon
		// as the source is back, even with identical content.
		if e.SourcePresent && ImporterClosedReason(e.StatusReason) {
			return StatusActive, ReasonSourceReturned
		}
		if e.SourceChanged {
			return StatusActive, ReasonSourceReturned
		}
		return StatusClosed, ""
	}

	switch e.CurrentStatus {
	case StatusPending:
		if e.AllSourcesMissing {
			return StatusHidden, "vanished_before_review"
		}
		if e.Trusted && e.Quality >= p.AutoActivate {
			return StatusActive, "auto_quality"
		}
		if e.Quality >= p.KeepPending {
			return StatusPending, "awaiting_review"
		}
		// Below KeepPending a pending spot shouldn't exist — planner drops it.
		return StatusHidden, "below_quality_floor"
	case StatusActive:
		if e.AllSourcesMissing && e.MissingRuns >= p.CloseAfterMissing && !e.RecentConfirm {
			return StatusClosed, "source_missing"
		}
	}
	return e.CurrentStatus, ""
}

// NewSpotStatus decides the initial status for a candidate that has no
// existing spot row yet.
func (p Policy) NewSpotStatus(e Evidence) (Status, string) {
	if e.Trusted && e.Quality >= p.AutoActivate {
		return StatusActive, "auto_quality"
	}
	if e.Quality >= p.KeepPending {
		return StatusPending, "awaiting_review"
	}
	return "", "below_quality_floor" // stays in raw layer
}
