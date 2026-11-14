package spots

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

const (
	// MaxBBoxDeltaDeg caps the viewport so zoomed-out requests stay cheap.
	// 0.5° is roughly 55 km at the equator — city-pack scale.
	MaxBBoxDeltaDeg = 0.5

	// ForecastDays is the spec'd 7-day hourly forecast window.
	ForecastDays = 7

	// MaxBatchIDs caps GET /v1/spots?ids=.
	MaxBatchIDs = 50
	// MaxScoreHours caps the hourly series (GET /v1/spots?hours=).
	MaxScoreHours = 24
)

// WeatherSourceCode maps a stored provider name to the public attribution
// code the app renders ("Apple Weather" / "Open-Meteo"); nil for none/unknown.
func WeatherSourceCode(provider string) *string {
	var code string
	switch provider {
	case "weatherkit":
		code = "weatherkit"
	case "open-meteo", "openmeteo":
		code = "openmeteo"
	default:
		return nil
	}
	return &code
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListInBBox returns active spots in the viewport with the score at the
// requested hour (quantized to the hour for edge-cache friendliness).
func (s *Service) ListInBBox(ctx context.Context, bbox BBox, when *time.Time) ([]SpotWithScore, error) {
	return s.List(ctx, ListQuery{BBox: &bbox, When: when})
}

// ListResult is a viewport/batch page. Total is the number of matching
// spots before any cap; Truncated is true when Items holds only the top
// part of them.
type ListResult struct {
	Items     []SpotWithScore
	Total     int
	Truncated bool
}

// List serves ids / viewport lookups for internal callers (share page, MCP):
// by viewport or by ids, optionally with the simplified outline and an hourly
// score series. It never truncates.
func (s *Service) List(ctx context.Context, q ListQuery) ([]SpotWithScore, error) {
	res, err := s.list(ctx, q, false)
	return res.Items, err
}

// ListPage serves GET /v1/spots. Same as List, except wide viewports are
// capped to the best SpotCap spots (see SpotCap) so zoomed-out responses stay
// small enough to cache. The cap is a pure function of the canonical query
// (bbox span, hours, include), so the response cache key already covers it.
func (s *Service) ListPage(ctx context.Context, q ListQuery) (ListResult, error) {
	return s.list(ctx, q, true)
}

func (s *Service) list(ctx context.Context, q ListQuery, capWide bool) (ListResult, error) {
	if (q.BBox == nil) == (len(q.IDs) == 0) {
		return ListResult{}, ErrInvalidQuery
	}
	if q.BBox != nil {
		if err := validateBBox(*q.BBox); err != nil {
			return ListResult{}, err
		}
	}
	if len(q.IDs) > MaxBatchIDs || q.Hours < 0 || q.Hours > MaxScoreHours {
		return ListResult{}, ErrInvalidQuery
	}
	now := time.Now().UTC()
	ts := now
	switch {
	case q.When != nil:
		ts = q.When.UTC()
	case q.From != nil:
		ts = q.From.UTC()
	}
	ts = ts.Truncate(time.Hour)

	var from, to time.Time // empty range unless an hourly series is wanted
	if q.Hours > 0 {
		from = now
		if q.From != nil {
			from = q.From.UTC()
		}
		from = from.Truncate(time.Hour)
		to = from.Add(time.Duration(q.Hours) * time.Hour)
	}
	limit := 0
	if capWide && q.BBox != nil {
		limit = SpotCap(*q.BBox, q.Hours, q.IncludeArea)
	}
	items, total, err := s.repo.List(ctx, q, ts, from, to, limit)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Items: items, Total: total, Truncated: limit > 0 && total > len(items)}, nil
}

const (
	// WideBBoxDeg is the viewport span (degrees, canonical box) above which
	// GET /v1/spots stops returning every spot. The iOS map asks for outlines
	// only below a 0.05 deg region (+0.01 padding and up to 0.02 grid snap per
	// side = ~0.11 deg request), so everything the app sends with area, and
	// ordinary city-district views, stay uncapped.
	WideBBoxDeg = 0.12

	// ResponseBudgetBytes is the target identity-JSON size of a capped
	// response: well under the 1 MiB response-cache entry cap, leaving
	// headroom for spots with long names or large outlines.
	ResponseBudgetBytes = 640 << 10

	// Measured JSON bytes per spot (Sydney, the densest pack): a plain
	// feature is ~520 B, each requested hour adds ~64 B, and an outline adds
	// ~370 B on average.
	featureBaseBytes = 520
	featureHourBytes = 64
	featureAreaBytes = 370

	// minSpotCap keeps a capped view useful even for the heaviest shape.
	minSpotCap = 100
)

// SpotCap returns how many spots a viewport response may hold, or 0 for no
// cap. Viewports up to WideBBoxDeg return everything; wider ones return the
// top N by score, N sized so the JSON stays near ResponseBudgetBytes given
// the per-spot weight of the requested hourly series / outlines.
func SpotCap(b BBox, hours int, includeArea bool) int {
	if max(b.East-b.West, b.North-b.South) <= WideBBoxDeg+1e-9 {
		return 0
	}
	per := featureBaseBytes + featureHourBytes*hours
	if includeArea {
		per += featureAreaBytes
	}
	return max(ResponseBudgetBytes/per, minSpotCap)
}

// ListPacks returns the city packs the app can use (beta/live).
func (s *Service) ListPacks(ctx context.Context) ([]Pack, error) {
	return s.repo.ListPacks(ctx)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (SpotDetail, error) {
	detail, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return SpotDetail{}, err
	}
	switch detail.Status {
	case "merged":
		if detail.MergedInto != nil {
			return SpotDetail{}, MergedError{NewID: *detail.MergedInto}
		}
		return SpotDetail{}, ErrNotFound
	case "active":
	default:
		return SpotDetail{}, ErrNotFound
	}

	detail.Amenities, err = s.repo.ListAmenities(ctx, id)
	if err != nil {
		return SpotDetail{}, err
	}
	detail.Sources, err = s.repo.ListSources(ctx, id)
	if err != nil {
		return SpotDetail{}, err
	}
	// Weather rows live on the coarser weather grid, like the scorer reads them.
	provider, err := s.repo.WeatherProvider(ctx, geo.WeatherCellID(detail.CellID))
	if err != nil {
		return SpotDetail{}, err
	}
	detail.WeatherSource = WeatherSourceCode(provider)
	return detail, nil
}

// Forecast returns hourly scores for the next ForecastDays days starting at
// the current hour.
func (s *Service) Forecast(ctx context.Context, id uuid.UUID) ([]ForecastPoint, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	from := time.Now().UTC().Truncate(time.Hour)
	to := from.Add(ForecastDays * 24 * time.Hour)
	points, err := s.repo.ListForecast(ctx, id, geo.WeatherCellID(detail.CellID), from, to)
	if err != nil {
		return nil, err
	}
	for i := range points {
		points[i].TSHour = points[i].TSHour.UTC()
	}
	return points, nil
}

// BBoxGridDeg is the viewport quantum used to canonicalize bboxes for
// caching: the app sends a slightly different float bbox on every map drag,
// so raw values would mint a new cache key per request. Expanding to the
// grid returns a few extra edge spots but makes nearby views share entries.
const BBoxGridDeg = 0.02

// CanonicalBBox expands b outward to the BBoxGridDeg grid (west/south down,
// east/north up) and clamps to WGS84 bounds. The result is only used when the
// raw box is itself valid AND the expansion stays valid — otherwise the raw
// box is returned so validation/errors behave exactly as before.
func CanonicalBBox(b BBox) BBox {
	if validateBBox(b) != nil {
		return b
	}
	out := BBox{
		West:  gridFloor(b.West),
		South: gridFloor(b.South),
		East:  gridCeil(b.East),
		North: gridCeil(b.North),
	}
	out.West = max(out.West, -180)
	out.East = min(out.East, 180)
	out.South = max(out.South, -90)
	out.North = min(out.North, 90)
	if validateBBox(out) != nil {
		return b
	}
	return out
}

func gridFloor(v float64) float64 { return math.Floor(v/BBoxGridDeg+1e-9) * BBoxGridDeg }
func gridCeil(v float64) float64  { return math.Ceil(v/BBoxGridDeg-1e-9) * BBoxGridDeg }

func validateBBox(b BBox) error {
	if b.West >= b.East || b.South >= b.North {
		return ErrInvalidBBox
	}
	if b.West < -180 || b.East > 180 || b.South < -90 || b.North > 90 {
		return ErrInvalidBBox
	}
	if b.East-b.West > MaxBBoxDeltaDeg || b.North-b.South > MaxBBoxDeltaDeg {
		return ErrBboxTooLarge
	}
	return nil
}
