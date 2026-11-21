package osm

type Status string

const (
	StatusPending Status = "pending"
	StatusActive  Status = "active"
	StatusClosed  Status = "closed"
	StatusHidden  Status = "hidden"
	StatusMerged  Status = "merged"
)

type Policy struct {
	AutoActivate      int
	KeepPending       int
	CloseAfterMissing int
	MaxCloseFraction  float64
	ConfirmShieldDays int
	MinTablesForSite  int

	OrphanClusterEpsM float64
}

var DefaultPolicy = Policy{
	AutoActivate:      60,
	KeepPending:       35,
	CloseAfterMissing: 2,
	MaxCloseFraction:  0.15,
	ConfirmShieldDays: 180,
	MinTablesForSite:  3,
	OrphanClusterEpsM: 80,
}

type Evidence struct {
	CurrentStatus     Status
	Quality           int
	Trusted           bool
	MissingRuns       int
	AllSourcesMissing bool
	RecentConfirm     bool
	SourceChanged     bool
	ClosedByReport    bool
	Curated           bool

	StatusReason string

	SourcePresent bool
}

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
	ReasonInsideCampsite  = "inside_campsite"
)

func ImporterHiddenReason(r string) bool {
	switch r {
	case ReasonBelowFloor, ReasonVanished, ReasonSourcesMoved, ReasonInsideCampsite:
		return true
	}
	return false
}

func ImporterClosedReason(r string) bool {
	return r == ReasonSourceMissing || r == ReasonSourcesMoved
}

func (p Policy) NextStatus(e Evidence) (Status, string) {

	if e.Curated {
		return e.CurrentStatus, "curated"
	}
	switch e.CurrentStatus {
	case StatusMerged:
		return e.CurrentStatus, ""
	case StatusHidden:

		if !ImporterHiddenReason(e.StatusReason) || !e.SourcePresent {
			return e.CurrentStatus, ""
		}
		if st, reason := p.NewSpotStatus(e); st != "" {
			return st, reason
		}
		return StatusHidden, e.StatusReason
	case StatusClosed:
		if e.ClosedByReport {
			return StatusClosed, ReasonClosedByReport
		}

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

		return StatusHidden, "below_quality_floor"
	case StatusActive:
		if e.AllSourcesMissing && e.MissingRuns >= p.CloseAfterMissing && !e.RecentConfirm {
			return StatusClosed, "source_missing"
		}
	}
	return e.CurrentStatus, ""
}

func (p Policy) NewSpotStatus(e Evidence) (Status, string) {
	if e.Trusted && e.Quality >= p.AutoActivate {
		return StatusActive, "auto_quality"
	}
	if e.Quality >= p.KeepPending {
		return StatusPending, "awaiting_review"
	}
	return "", "below_quality_floor"
}
