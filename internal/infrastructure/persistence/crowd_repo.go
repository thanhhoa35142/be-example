package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/thanhhoa35142/be-example/internal/modules/crowd"
	"github.com/thanhhoa35142/be-example/internal/modules/holidays"
)

type CrowdRepo struct {
	q *Queries
}

func NewCrowdRepo(q *Queries) *CrowdRepo {
	return &CrowdRepo{q: q}
}

func f4p(v pgtype.Float4) *float64 {
	if !v.Valid {
		return nil
	}
	f := float64(v.Float32)
	return &f
}

func i2p(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int16)
	return &i
}

func (r *CrowdRepo) Spots(ctx context.Context) ([]crowd.Spot, error) {
	rows, err := r.q.ListCrowdSpots(ctx)
	if err != nil {
		return nil, err
	}
	spots := make([]crowd.Spot, len(rows))
	for i, row := range rows {
		spots[i] = crowd.Spot{
			ID: row.ID, Kind: row.Kind, CityPackID: row.CityPackID,
			Country: row.Country, CellID: row.CellID, Tz: row.Tz,
			AreaM2: row.AreaM2,
		}
	}
	return spots, nil
}

func (r *CrowdRepo) Curves(ctx context.Context) (map[crowd.CurveKey]float64, error) {
	rows, err := r.q.ListCrowdCurves(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[crowd.CurveKey]float64, len(rows))
	for _, row := range rows {
		m[crowd.CurveKey{Kind: row.Kind, Dow: int(row.Dow), Hour: int(row.Hour)}] = float64(row.Factor)
	}
	return m, nil
}

func (r *CrowdRepo) Weather(ctx context.Context, cellIDs []int64, from, to time.Time) ([]crowd.WeatherPoint, error) {
	rows, err := r.q.ListCrowdWeather(ctx, ListCrowdWeatherParams{
		Column1: cellIDs, Ts: from, Ts_2: to,
	})
	if err != nil {
		return nil, err
	}
	points := make([]crowd.WeatherPoint, len(rows))
	for i, row := range rows {
		points[i] = crowd.WeatherPoint{
			CellID: row.CellID, TS: row.Ts,
			TempC: f4p(row.Temp), FeelsLikeC: f4p(row.FeelsLike), PrecipProb: i2p(row.PrecipProb),
		}
	}
	return points, nil
}

func (r *CrowdRepo) Holidays(ctx context.Context, countries []string, from, to time.Time) ([]crowd.Holiday, error) {
	rows, err := r.q.ListCrowdHolidays(ctx, ListCrowdHolidaysParams{
		Column1: countries,
		Date:    pgtype.Date{Time: from, Valid: true},
		Date_2:  pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	holidays := make([]crowd.Holiday, len(rows))
	for i, row := range rows {
		holidays[i] = crowd.Holiday{
			Country: row.Country,
			Date:    row.Date.Time,
			Kind:    row.Kind,
		}
	}
	return holidays, nil
}

func (r *CrowdRepo) Upsert(ctx context.Context, spotID uuid.UUID, dow, hour int, level int) error {
	return r.q.UpsertCrowdEstimate(ctx, UpsertCrowdEstimateParams{
		SpotID: spotID, Dow: int16(dow), Hour: int16(hour), Level: int16(level),
	})
}

func (r *CrowdRepo) CommunityStats(ctx context.Context, minReports int) ([]crowd.CommunitySlot, error) {
	rows, err := r.q.AggregateCommunityCrowd(ctx, minReports)
	if err != nil {
		return nil, err
	}
	slots := make([]crowd.CommunitySlot, len(rows))
	for i, row := range rows {
		var avg float64
		if row.AvgLevel.Valid {
			f, err := row.AvgLevel.Float64Value()
			if err != nil {
				return nil, err
			}
			avg = f.Float64
		}
		slots[i] = crowd.CommunitySlot{
			SpotID: row.SpotID, Dow: int(row.LocalDow), Hour: int(row.LocalHour),
			NReports: int(row.NReports), AvgLevel: avg,
		}
	}
	return slots, nil
}

func (r *CrowdRepo) UpsertCommunity(ctx context.Context, spotID uuid.UUID, dow, hour, level, nReports int) error {
	return r.q.UpsertCommunityEstimate(ctx, UpsertCommunityEstimateParams{
		SpotID: spotID, Dow: int16(dow), Hour: int16(hour),
		Level: int16(level), NReports: int32(nReports),
	})
}

func (r *CrowdRepo) DeleteStaleCommunity(ctx context.Context) error {
	return r.q.DeleteStaleCommunityEstimates(ctx)
}

// HolidaysRepo implements holidays.Repository.
type HolidaysRepo struct{ q *Queries }

func NewHolidaysRepo(q *Queries) *HolidaysRepo { return &HolidaysRepo{q: q} }

func (r *HolidaysRepo) Countries(ctx context.Context) ([]string, error) {
	return r.q.ListHolidayCountries(ctx)
}

func (r *HolidaysRepo) Upsert(ctx context.Context, rows []holidays.Row) (int64, error) {
	type jsonRow struct {
		Country string `json:"country"`
		Date    string `json:"date"`
		Name    string `json:"name"`
	}
	payload := make([]jsonRow, len(rows))
	for i, row := range rows {
		payload[i] = jsonRow{row.Country, row.Date.Format("2006-01-02"), row.Name}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return r.q.UpsertHolidays(ctx, raw)
}
