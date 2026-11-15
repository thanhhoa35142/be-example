package spots

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

const (
	MaxBBoxDeltaDeg = 0.5

	ForecastDays = 7

	MaxBatchIDs = 50

	MaxScoreHours = 24
)

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

func (s *Service) ListInBBox(ctx context.Context, bbox BBox, when *time.Time) ([]SpotWithScore, error) {
	return s.List(ctx, ListQuery{BBox: &bbox, When: when})
}

type ListResult struct {
	Items     []SpotWithScore
	Total     int
	Truncated bool
}

func (s *Service) List(ctx context.Context, q ListQuery) ([]SpotWithScore, error) {
	res, err := s.list(ctx, q, false)
	return res.Items, err
}

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

	var from, to time.Time
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
	WideBBoxDeg = 0.12

	ResponseBudgetBytes = 640 << 10

	featureBaseBytes = 520
	featureHourBytes = 64
	featureAreaBytes = 370

	minSpotCap = 100
)

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

	provider, err := s.repo.WeatherProvider(ctx, geo.WeatherCellID(detail.CellID))
	if err != nil {
		return SpotDetail{}, err
	}
	detail.WeatherSource = WeatherSourceCode(provider)
	return detail, nil
}

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

const BBoxGridDeg = 0.02

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
