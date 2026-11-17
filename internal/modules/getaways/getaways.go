package getaways

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
)

var (
	ErrInvalidStart = errors.New("getaways: start outside [today-1, today+6]")
	ErrInvalidRange = errors.New("getaways: days outside 1-3 or range past today+6")
	ErrInvalidLimit = errors.New("getaways: limit outside 1-39")
)

const (
	DefaultDays  = 2
	MaxDays      = 3
	DefaultLimit = 10
	MaxLimit     = 39
	// FreeLimit is the most cities the Free plan sees; Plus sees up to MaxLimit.
	FreeLimit = 3

	// MinPackSpots is the active-spot minimum for a pack to take part (a
	// median over a handful of spots is noisy).
	MinPackSpots = 30
	// MinCoverage is the share of a pack's active spots that must have a
	// weather row for every hour of a local day, else the day is dropped.
	MinCoverage = 0.8

	// MaxStartOffsetDays: start may be at most this many days after today (UTC);
	// the whole range must also end by then (7-day forecast horizon).
	MaxStartOffsetDays = 6

	WindowMinHours = 2
	WindowMaxHours = 4

	// GoodDayMinHours is how many good hours (weather score >= GoodScore) a
	// local day needs to count in WindowDays. A barely good 2-hour day still
	// shows in dayScores but must not outrank a city with one great day.
	GoodDayMinHours = 4

	// First and last start hour (local) that is stored and ranked.
	FirstHour = digest.DayStartHour
	LastHour  = digest.DayEndHour - 1

	DefaultGoodScore = 65
	DefaultHintScore = 80

	ReasonDryDay     = "getaway.dry_day"
	ReasonLongWindow = "getaway.long_window"

	PlanPlus = "plus"
)

// Config holds the tunables. HintScore is consumed by the app (Best Now hint
// "This weekend looks better in X"); the server only carries the value in
// config next to GoodScore and does not use it.
type Config struct {
	GoodScore int
	HintScore int
}

// Pack is a beta/live city pack with its active-spot count.
type Pack struct {
	ID          uuid.UUID
	Slug        string
	Name        string
	Country     string
	TZ          string
	ActiveSpots int
}

// Caps are the score caps of the active scoring config, keyed by the
// cap_reason values of scoring/scorer.go.
type Caps struct {
	Version  int16
	Rain     int
	Heat     int
	Cold     int
	Dark     int
	Twilight int
}

// CityHour is one row of t_city_hour_outlook.
type CityHour struct {
	TSHour       time.Time
	LocalDate    time.Time // civil date at UTC midnight
	WeatherScore int
	RainShare    float64
	SpotsN       int
	TopSpotID    uuid.UUID // uuid.Nil when unknown
	TopSpotScore int
}

// OutlookRow is a stored hour joined with its pack, as read for List.
type OutlookRow struct {
	PackID  uuid.UUID
	Slug    string
	Name    string
	Country string
	TZ      string
	CityHour
	ComputedAt time.Time
}

type SpotInfo struct {
	ID   uuid.UUID
	Name *string
}

type Reason struct {
	Code   string
	Params map[string]any
}

// RefreshStats reports one Refresh run. Written is the number of hour rows
// stored; the worker chains getaways.refresh after scoring.recompute only when
// scoring wrote rows.
type RefreshStats struct {
	Packs       int
	Eligible    int
	Ineligible  int
	Refreshed   int
	Written     int
	DaysDropped int
	Failed      int
}

type Query struct {
	// Start is a civil date (the time of day is ignored), applied in each
	// city's time zone. Zero means the default start.
	Start time.Time
	Days  int
	Limit int
	// Plan is the caller's plan ("plus" or anything else = Free).
	Plan string
}

type Result struct {
	Start      time.Time
	Days       int
	Items      []Item
	ComputedAt *time.Time
}

type Item struct {
	Slug       string
	Name       string
	Country    string
	TZ         string
	Lon, Lat   float64
	WindowDays int
	BestDay    time.Time
	Window     Window
	GoodHours  int
	DayScores  []DayScore
	TopSpot    *TopSpot
	Reasons    []Reason
}

// Window is the city's best run of good hours on a day. WeatherScore is the
// rounded mean city weather score (the number the ranking uses); it is not
// the total score of any spot.
type Window struct {
	Start        time.Time
	End          time.Time
	WeatherScore int
}

// DayScore.WeatherScore is nil for a day with no window.
type DayScore struct {
	Date         time.Time
	WeatherScore *int
	GoodHours    int
}

// TopSpot is the named spot the city card points at. Score is that spot's
// total score (weather, crowd, amenities) at the window's best hour, so it is
// usually lower than the window's weather score; the app shows both.
type TopSpot struct {
	ID    uuid.UUID
	Name  *string
	Score int
}
