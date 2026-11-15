package crowd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

const writeBatchSize = 10000

type Stats struct {
	Spots   int
	Slots   int
	Written int
	Skipped int

	Deleted int
}

func (s *Service) Refresh(ctx context.Context) (Stats, error) {
	spots, err := s.repo.Spots(ctx)
	if err != nil {
		return Stats{}, err
	}
	curves, err := s.repo.Curves(ctx)
	if err != nil {
		return Stats{}, err
	}
	if len(spots) == 0 {
		return Stats{}, nil
	}

	from := s.now().UTC().Add(-24 * time.Hour)
	to := s.now().UTC().Add(8 * 24 * time.Hour)

	cellSet := map[int64]struct{}{}
	countrySet := map[string]struct{}{}
	for _, sp := range spots {

		cellSet[geo.WeatherCellID(sp.CellID)] = struct{}{}
		countrySet[sp.Country] = struct{}{}
	}
	cellIDs := make([]int64, 0, len(cellSet))
	for c := range cellSet {
		cellIDs = append(cellIDs, c)
	}
	countries := make([]string, 0, len(countrySet))
	for c := range countrySet {
		countries = append(countries, c)
	}

	weatherRows, err := s.repo.Weather(ctx, cellIDs, from, to)
	if err != nil {
		return Stats{}, fmt.Errorf("weather: %w", err)
	}
	weatherIdx := indexWeather(weatherRows)

	holRows, err := s.repo.Holidays(ctx, countries, from, to)
	if err != nil {
		return Stats{}, fmt.Errorf("holidays: %w", err)
	}
	holIdx := indexHolidays(holRows)

	type estimate struct {
		spot Spot
		raw  [7][24]float64
	}
	ests := make([]estimate, 0, len(spots))
	for _, sp := range spots {
		raw, err := s.estimateSpot(sp, curves, weatherIdx, holIdx)
		if err != nil {
			slog.Warn("crowd: skip spot", "spot_id", sp.ID, "err", err)
			continue
		}
		ests = append(ests, estimate{spot: sp, raw: raw})
	}

	peaks := map[peakKey]float64{}
	for _, e := range ests {
		for _, row := range e.raw {
			for _, v := range row {
				k := peakKey{kind: e.spot.Kind, city: e.spot.CityPackID}
				if v > peaks[k] {
					peaks[k] = v
				}
			}
		}
	}

	var st Stats
	st.Spots = len(ests)
	batch := make([]ModelEstimate, 0, writeBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := s.repo.UpsertModelBatch(ctx, batch)
		if err != nil {
			return fmt.Errorf("upsert crowd batch: %w", err)
		}
		st.Written += int(n)
		batch = batch[:0]
		return nil
	}
	for _, e := range ests {
		peak := peaks[peakKey{kind: e.spot.Kind, city: e.spot.CityPackID}]
		for dow := 0; dow < 7; dow++ {
			for hour := 0; hour < 24; hour++ {
				batch = append(batch, ModelEstimate{
					SpotID: e.spot.ID, Dow: dow, Hour: hour,
					Level: Normalize(e.raw[dow][hour], peak),
				})
				st.Slots++
				if len(batch) >= writeBatchSize {
					if err := flush(); err != nil {
						return st, err
					}
				}
			}
		}
	}
	if err := flush(); err != nil {
		return st, err
	}
	st.Skipped = len(spots) - len(ests)
	return st, nil
}

type peakKey struct {
	kind string
	city uuid.UUID
}

func (s *Service) estimateSpot(
	sp Spot,
	curves map[CurveKey]float64,
	weatherIdx map[int64]map[time.Time]WeatherPoint,
	holIdx map[string]map[string]string,
) ([7][24]float64, error) {
	var raw [7][24]float64

	loc, err := time.LoadLocation(sp.Tz)
	if err != nil {
		return raw, fmt.Errorf("timezone %q: %w", sp.Tz, err)
	}
	now := s.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	todayDow := int(now.Weekday())

	baseline := Baseline(sp.Kind, sp.AreaM2)
	cellWeather := weatherIdx[geo.WeatherCellID(sp.CellID)]

	for dow := 0; dow < 7; dow++ {

		date := today.AddDate(0, 0, (dow-todayDow+7)%7)
		dateKey := date.Format("2006-01-02")
		holKind := holIdx[sp.Country][dateKey]
		calFactor := CalendarFactor(holKind)

		for hour := 0; hour < 24; hour++ {
			local := time.Date(date.Year(), date.Month(), date.Day(), hour, 0, 0, 0, loc)
			w := cellWeather[local.UTC().Truncate(time.Hour)]

			curveFactor := 1.0
			if f, ok := curves[CurveKey{Kind: sp.Kind, Dow: dow, Hour: hour}]; ok {
				curveFactor = f
			}

			raw[dow][hour] = baseline * curveFactor *
				WeatherFactor(w.TempC, w.FeelsLikeC, w.PrecipProb) * calFactor
		}
	}
	return raw, nil
}

const MinCommunityReports = 5

func (s *Service) AggregateCommunity(ctx context.Context) (Stats, error) {
	runStart := time.Now().UTC()
	slots, err := s.repo.CommunityStats(ctx, MinCommunityReports)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	for _, slot := range slots {

		level := int((slot.AvgLevel-1)*40 + 10 + 0.5)
		if level < 0 {
			level = 0
		}
		if level > 100 {
			level = 100
		}
		if err := s.repo.UpsertCommunity(ctx, slot.SpotID, slot.Dow, slot.Hour, level, slot.NReports); err != nil {
			return st, err
		}
		st.Written++
	}
	n, err := s.repo.DeleteStaleCommunity(ctx, runStart)
	if err != nil {
		return st, err
	}
	st.Deleted = int(n)
	return st, nil
}

func indexWeather(rows []WeatherPoint) map[int64]map[time.Time]WeatherPoint {
	m := make(map[int64]map[time.Time]WeatherPoint, 8)
	for _, r := range rows {
		byTS, ok := m[r.CellID]
		if !ok {
			byTS = map[time.Time]WeatherPoint{}
			m[r.CellID] = byTS
		}
		byTS[r.TS.UTC().Truncate(time.Hour)] = r
	}
	return m
}

func indexHolidays(rows []Holiday) map[string]map[string]string {
	m := make(map[string]map[string]string, 8)
	for _, h := range rows {
		byDate, ok := m[h.Country]
		if !ok {
			byDate = map[string]string{}
			m[h.Country] = byDate
		}
		key := h.Date.UTC().Format("2006-01-02")

		if h.Kind == "public" || byDate[key] != "public" {
			byDate[key] = h.Kind
		}
	}
	return m
}
