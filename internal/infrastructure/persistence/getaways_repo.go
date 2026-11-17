package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/getaways"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
)

type GetawaysRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewGetawaysRepo(q *Queries, pool *pgxpool.Pool) *GetawaysRepo {
	return &GetawaysRepo{q: q, pool: pool}
}

func (r *GetawaysRepo) ActiveCaps(ctx context.Context) (getaways.Caps, error) {
	row, err := r.q.GetActiveScoringConfig(ctx)
	if err != nil {
		return getaways.Caps{}, err
	}
	cfg, err := scoring.ParseConfig(row.Version, row.Weights, row.Thresholds)
	if err != nil {
		return getaways.Caps{}, err
	}
	th := cfg.Thresholds
	return getaways.Caps{
		Version: cfg.Version,
		Rain:    th.RainScoreCap, Heat: th.HeatScoreCap, Cold: th.ColdScoreCap,
		Dark: th.DarkScoreCap, Twilight: th.TwilightScoreCap,
	}, nil
}

func (r *GetawaysRepo) Packs(ctx context.Context) ([]getaways.Pack, error) {
	rows, err := r.q.ListGetawayPacks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]getaways.Pack, len(rows))
	for i, p := range rows {
		out[i] = getaways.Pack{ID: p.ID, Slug: p.Slug, Name: p.Name, Country: p.Country, TZ: p.Tz, ActiveSpots: int(p.ActiveSpots)}
	}
	return out, nil
}

func (r *GetawaysRepo) ComputeHours(ctx context.Context, packID uuid.UUID, caps getaways.Caps, from, to time.Time) ([]getaways.CityHour, error) {
	rows, err := r.q.ComputeCityHours(ctx, ComputeCityHoursParams{
		RainCap: int32(caps.Rain), HeatCap: int32(caps.Heat), ColdCap: int32(caps.Cold),
		DarkCap: int32(caps.Dark), TwilightCap: int32(caps.Twilight),
		PackID: packID, FromTs: from, ToTs: to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]getaways.CityHour, len(rows))
	for i, h := range rows {
		out[i] = getaways.CityHour{
			TSHour: h.TsHour.UTC(), LocalDate: h.LocalDate.Time,
			WeatherScore: int(h.WeatherScore), RainShare: float64(h.RainShare), SpotsN: int(h.SpotsN),
		}
		if h.TopSpotID.Valid {
			out[i].TopSpotID = uuid.UUID(h.TopSpotID.Bytes)
			out[i].TopSpotScore = int(h.TopSpotScore.Int16)
		}
	}
	return out, nil
}

func (r *GetawaysRepo) ReplaceHours(ctx context.Context, packID uuid.UUID, from time.Time, hours []getaways.CityHour, configVersion int16, computedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	if err := q.DeleteOutlookFrom(ctx, DeleteOutlookFromParams{PackID: packID, FromTs: from}); err != nil {
		return err
	}
	if n := len(hours); n > 0 {
		p := InsertOutlookHoursParams{
			PackID: packID, ConfigVersion: configVersion, ComputedAt: computedAt,
			TsHours: make([]time.Time, n), LocalDates: make([]pgtype.Date, n),
			WeatherScores: make([]int16, n), RainShares: make([]float32, n), SpotsNs: make([]int32, n),
			TopSpotIds: make([]uuid.UUID, n), TopSpotScores: make([]int16, n),
		}
		for i, h := range hours {
			p.TsHours[i] = h.TSHour
			p.LocalDates[i] = pgDate(h.LocalDate)
			p.WeatherScores[i] = int16(h.WeatherScore)
			p.RainShares[i] = float32(h.RainShare)
			p.SpotsNs[i] = int32(h.SpotsN)
			p.TopSpotIds[i] = h.TopSpotID
			p.TopSpotScores[i] = int16(h.TopSpotScore)
		}
		if err := q.InsertOutlookHours(ctx, p); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *GetawaysRepo) DeleteExceptPacks(ctx context.Context, keep []uuid.UUID) (int64, error) {
	if keep == nil {
		keep = []uuid.UUID{}
	}
	return r.q.DeleteOutlookExceptPacks(ctx, keep)
}

func (r *GetawaysRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	return r.q.DeleteOutlookBefore(ctx, before)
}

func (r *GetawaysRepo) Outlook(ctx context.Context, fromDate, toDate, notBefore time.Time) ([]getaways.OutlookRow, error) {
	rows, err := r.q.ListOutlookHours(ctx, ListOutlookHoursParams{FromDate: pgDate(fromDate), ToDate: pgDate(toDate), NotBefore: notBefore})
	if err != nil {
		return nil, err
	}
	out := make([]getaways.OutlookRow, len(rows))
	for i, h := range rows {
		ch := getaways.CityHour{
			TSHour: h.TsHour.UTC(), LocalDate: h.LocalDate.Time,
			WeatherScore: int(h.WeatherScore), RainShare: float64(h.RainShare),
		}
		if h.TopSpotID.Valid {
			ch.TopSpotID = uuid.UUID(h.TopSpotID.Bytes)
			ch.TopSpotScore = int(h.TopSpotScore.Int16)
		}
		out[i] = getaways.OutlookRow{
			PackID: h.PackID, Slug: h.Slug, Name: h.Name, Country: h.Country, TZ: h.Tz,
			CityHour: ch, ComputedAt: h.ComputedAt,
		}
	}
	return out, nil
}

func (r *GetawaysRepo) Centers(ctx context.Context, packIDs []uuid.UUID) (map[uuid.UUID][2]float64, error) {
	rows, err := r.q.ListPackCenters(ctx, packIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][2]float64, len(rows))
	for _, c := range rows {
		out[c.ID] = [2]float64{c.CenterLon, c.CenterLat}
	}
	return out, nil
}

func (r *GetawaysRepo) Spots(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]getaways.SpotInfo, error) {
	rows, err := r.q.ListGetawaySpots(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]getaways.SpotInfo, len(rows))
	for _, s := range rows {
		info := getaways.SpotInfo{ID: s.ID}
		if s.Name.Valid {
			n := s.Name.String
			info.Name = &n
		}
		out[s.ID] = info
	}
	return out, nil
}

func (r *GetawaysRepo) SpotReasons(ctx context.Context, spotID uuid.UUID, tsHour time.Time) ([]getaways.Reason, error) {
	raw, err := r.q.GetSpotScoreReasons(ctx, GetSpotScoreReasonsParams{SpotID: spotID, TsHour: tsHour})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rs []struct {
		Code   string         `json:"code"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("score reasons: %w", err)
	}
	out := make([]getaways.Reason, len(rs))
	for i, x := range rs {
		out[i] = getaways.Reason{Code: x.Code, Params: x.Params}
	}
	return out, nil
}
