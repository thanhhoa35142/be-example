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
}

// NextStatus is the pure state-machine decision for one spot (INGEST_STRATEGY §4).
// Returns the new status and a machine-readable reason for t_spots.status_reason.
func (p Policy) NextStatus(e Evidence) (Status, string) {
	// Human/terminal states the importer must never move.
	switch e.CurrentStatus {
	case StatusHidden, StatusMerged:
		return e.CurrentStatus, ""
	case StatusClosed:
		if e.ClosedByReport {
			return StatusClosed, "closed_by_report" // reopen is admin-only
		}
		if e.SourceChanged {
			return StatusActive, "source_returned"
		}
		return StatusClosed, ""
	}

	if e.Curated {
		return e.CurrentStatus, "curated"
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
