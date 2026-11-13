package scoring

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

// ForecastHorizon is how far ahead scores are precomputed (spec: 7 days).
const ForecastHorizon = 7 * 24 * time.Hour

// amenityPoints maps amenity types to base scores when within 200 m.
var amenityPoints = map[string]int{
	"toilet":     30,
	"water":      20,
	"shelter":    15,
	"parking":    15,
	"playground": 20,
	"bbq":        20,
	"shower":     15,
	"dog_water":  10,
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

type Stats struct {
	Spots   int
	Hours   int
	Written int
	Skipped int
}

// Recompute recalculates spot_score for every active spot over the next
// ForecastHorizon hours. The upsert only writes changed rows.
func (s *Service) Recompute(ctx context.Context) (Stats, error) {
	cfg, err := s.repo.ActiveConfig(ctx)
	if err != nil {
		return Stats{}, err
	}

	spots, err := s.repo.ActiveSpots(ctx)
	if err != nil {
		return Stats{}, err
	}
	if len(spots) == 0 {
		return Stats{}, nil
	}

	spotIDs := make([]uuid.UUID, 0, len(spots))
	cellSet := make(map[int64]struct{})
	for _, sp := range spots {
		spotIDs = append(spotIDs, sp.ID)
		// Weather rows live on the coarser weather grid.
		cellSet[geo.WeatherCellID(sp.CellID)] = struct{}{}
	}
	cellIDs := make([]int64, 0, len(cellSet))
	for c := range cellSet {
		cellIDs = append(cellIDs, c)
	}

	now := time.Now().UTC()
	from := now.Truncate(time.Hour)
	to := from.Add(ForecastHorizon)

	weatherRows, err := s.repo.WeatherForCells(ctx, cellIDs, from.Add(-time.Hour), to)
	if err != nil {
		return Stats{}, err
	}
	crowdRows, err := s.repo.CrowdForSpots(ctx, spotIDs)
	if err != nil {
		return Stats{}, err
	}
	amenityRows, err := s.repo.AmenitiesForSpots(ctx, spotIDs)
	if err != nil {
		return Stats{}, err
	}

	weatherIdx := indexWeather(weatherRows)
	crowdIdx := indexCrowd(crowdRows)
	amenIdx := indexAmenities(amenityRows)

	stats := Stats{Spots: len(spots)}
	computedAt := now

	for _, sp := range spots {
		loc := time.UTC
		if l, err := time.LoadLocation(sp.TZ); err == nil {
			loc = l
		}
		amenScore := amenIdx[sp.ID]
		daysSince := daysSince(sp.LastVerifiedAt, now)
		weatherCell := geo.WeatherCellID(sp.CellID)

		for h := 0; h < int(ForecastHorizon/time.Hour); h++ {
			ts := from.Add(time.Duration(h) * time.Hour)
			local := ts.In(loc)

			in := Inputs{
				Weather:          weatherAt(weatherIdx, weatherCell, ts),
				Crowd:            crowdAt(crowdIdx, sp.ID, int(local.Weekday()), local.Hour()),
				AmenityScore:     amenScore,
				SunElevationDeg:  sunElevationMidHour(sp.Lat, sp.Lon, ts),
				DaysSinceUpdated: daysSince,
			}
			res := Score(in, cfg)
			if !res.HasData {
				stats.Skipped++
				continue
			}

			n, err := s.repo.UpsertScore(ctx, ScoreWrite{
				SpotID:        sp.ID,
				TSHour:        ts,
				ConfigVersion: cfg.Version,
				Score:         res.Score,
				SWeather:      res.Components.Weather,
				SCrowd:        res.Components.Crowd,
				SAmenity:      res.Components.Amenity,
				SShade:        res.Components.Shade,
				SFresh:        res.Components.Fresh,
				CapReason:     res.CapReason,
				Reasons:       res.Reasons,
				Confidence:    res.Confidence,
				ComputedAt:    computedAt,
			})
			if err != nil {
				return stats, err
			}
			stats.Hours++
			stats.Written += int(n)
		}
	}
	return stats, nil
}

// amenityScoreFor computes a 0-100 amenity score from nearby amenities.
// Full points within 200 m, half points up to ~1 km.
func amenityScoreFor(rows []AmenityRow) *int {
	if len(rows) == 0 {
		return nil
	}
	score := 0
	for _, r := range rows {
		pts, ok := amenityPoints[r.Type]
		if !ok {
			continue
		}
		if r.DistanceM <= 200 {
			score += pts
		} else if r.DistanceM <= 1000 {
			score += pts / 2
		}
	}
	if score == 0 {
		return nil
	}
	if score > 100 {
		score = 100
	}
	return &score
}

func daysSince(t *time.Time, now time.Time) int {
	if t == nil {
		return 365
	}
	d := int(now.Sub(*t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

type weatherKey struct {
	cellID int64
	ts     time.Time
}

func indexWeather(rows []WeatherRow) map[weatherKey]WeatherInput {
	m := make(map[weatherKey]WeatherInput, len(rows))
	for _, r := range rows {
		m[weatherKey{r.CellID, r.TS.UTC().Truncate(time.Hour)}] = WeatherInput{
			TempC:      r.TempC,
			FeelsLikeC: r.FeelsLikeC,
			PrecipProb: r.PrecipProb,
			WindKmh:    r.WindKmh,
			UV:         r.UV,
			Cloud:      r.Cloud,
		}
	}
	return m
}

func weatherAt(idx map[weatherKey]WeatherInput, cellID int64, ts time.Time) *WeatherInput {
	if w, ok := idx[weatherKey{cellID, ts.UTC().Truncate(time.Hour)}]; ok {
		return &w
	}
	return nil
}

type crowdKey struct {
	spotID uuid.UUID
	dow    int
	hour   int
}

func indexCrowd(rows []CrowdRow) map[crowdKey]CrowdInput {
	m := make(map[crowdKey]CrowdInput, len(rows))
	for _, r := range rows {
		m[crowdKey{r.SpotID, r.Dow, r.Hour}] = CrowdInput{Level: r.Level, Source: r.Source}
	}
	return m
}

// crowdAt picks the best available source for the slot: provider > community >
// model. crowd_estimate stores one row per (spot,dow,hour) so the source is
// already resolved upstream; this just looks it up.
func crowdAt(idx map[crowdKey]CrowdInput, spotID uuid.UUID, dow, hour int) *CrowdInput {
	if c, ok := idx[crowdKey{spotID, dow, hour}]; ok {
		return &c
	}
	return nil
}

func indexAmenities(rows []AmenityRow) map[uuid.UUID]*int {
	bySpot := make(map[uuid.UUID][]AmenityRow)
	for _, r := range rows {
		bySpot[r.SpotID] = append(bySpot[r.SpotID], r)
	}
	m := make(map[uuid.UUID]*int, len(bySpot))
	for id, rs := range bySpot {
		m[id] = amenityScoreFor(rs)
	}
	return m
}

// sunElevationMidHour is the solar elevation at ts+30min (the middle of the
// scored hour) at the spot.
func sunElevationMidHour(lat, lon float64, tsHour time.Time) *float64 {
	e := geo.SunElevationDeg(lat, lon, tsHour.Add(30*time.Minute))
	return &e
}
