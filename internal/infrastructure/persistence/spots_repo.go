package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

type SpotsRepository struct {
	queries *Queries
}

func NewSpotsRepository(q *Queries) *SpotsRepository {
	return &SpotsRepository{queries: q}
}

func (r *SpotsRepository) List(ctx context.Context, q spots.ListQuery, tsHour, seriesFrom, seriesTo time.Time) ([]spots.SpotWithScore, error) {
	if q.BBox != nil {
		rows, err := r.queries.ListSpotsInBBox(ctx, ListSpotsInBBoxParams{
			IncludeArea: q.IncludeArea,
			ScoresFrom:  seriesFrom,
			ScoresTo:    seriesTo,
			TsHour:      tsHour,
			West:        q.BBox.West,
			South:       q.BBox.South,
			East:        q.BBox.East,
			North:       q.BBox.North,
		})
		if err != nil {
			return nil, err
		}
		out := make([]spots.SpotWithScore, 0, len(rows))
		for _, row := range rows {
			out = append(out, spotWithScore(row.ID, row.CityPackID, row.Kind, row.Name, row.Names, row.Lon, row.Lat,
				row.Country, row.City, row.Tz, row.LastVerifiedAt, row.AreaGeojson, row.HourlyScores,
				row.Score, row.Confidence, row.CapReason, row.Reasons, row.ComputedAt))
		}
		return out, nil
	}

	rows, err := r.queries.ListSpotsByIDs(ctx, ListSpotsByIDsParams{
		Ids:         q.IDs,
		IncludeArea: q.IncludeArea,
		ScoresFrom:  seriesFrom,
		ScoresTo:    seriesTo,
		TsHour:      tsHour,
	})
	if err != nil {
		return nil, err
	}
	out := make([]spots.SpotWithScore, 0, len(rows))
	for _, row := range rows {
		out = append(out, spotWithScore(row.ID, row.CityPackID, row.Kind, row.Name, row.Names, row.Lon, row.Lat,
			row.Country, row.City, row.Tz, row.LastVerifiedAt, row.AreaGeojson, row.HourlyScores,
			row.Score, row.Confidence, row.CapReason, row.Reasons, row.ComputedAt))
	}
	return out, nil
}

func (r *SpotsRepository) WeatherProvider(ctx context.Context, weatherCellID int64) (string, error) {
	p, err := r.queries.GetSpotWeatherProvider(ctx, weatherCellID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return p, err
}

func (r *SpotsRepository) ListPacks(ctx context.Context) ([]spots.Pack, error) {
	rows, err := r.queries.ListPackSummaries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]spots.Pack, 0, len(rows))
	for _, row := range rows {
		out = append(out, spots.Pack{
			Slug: row.Slug, Name: row.Name, Country: row.Country, TZ: row.Tz,
			Center:      [2]float64{row.CenterLon, row.CenterLat},
			BBox:        [4]float64{row.West, row.South, row.East, row.North},
			ActiveSpots: int(row.ActiveSpots),
		})
	}
	return out, nil
}

func spotWithScore(id, packID uuid.UUID, kind string, name pgtype.Text, names []byte, lon, lat float64,
	country string, city pgtype.Text, tz string, verified pgtype.Timestamptz, area, hourly []byte,
	score, confidence pgtype.Int2, capReason pgtype.Text, reasons []byte, computedAt pgtype.Timestamptz,
) spots.SpotWithScore {
	return spots.SpotWithScore{
		Spot: spots.Spot{
			ID: id, CityPackID: packID, Kind: spots.Kind(kind), Name: textPtr(name),
			Names: namesFromJSON(names), Lon: lon, Lat: lat, Country: country,
			City: textPtr(city), TZ: tz, LastVerifiedAt: timePtr(verified),
		},
		Score:        scoreSnapshotFromRow(score, confidence, capReason, reasons, computedAt),
		Area:         area,
		HourlyScores: hourly,
	}
}

func (r *SpotsRepository) GetByID(ctx context.Context, id uuid.UUID) (spots.SpotDetail, error) {
	row, err := r.queries.GetSpotByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spots.SpotDetail{}, spots.ErrNotFound
		}
		return spots.SpotDetail{}, err
	}
	return spots.SpotDetail{
		Spot: spots.Spot{
			ID:             row.ID,
			CityPackID:     row.CityPackID,
			Kind:           spots.Kind(row.Kind),
			Name:           textPtr(row.Name),
			Names:          namesFromJSON(row.Names),
			Lon:            row.Lon,
			Lat:            row.Lat,
			Country:        row.Country,
			City:           textPtr(row.City),
			TZ:             row.Tz,
			LastVerifiedAt: timePtr(row.LastVerifiedAt),
		},
		Status:      row.Status,
		CellID:      row.CellID,
		MergedInto:  uuidPtr(row.MergedInto),
		AreaGeoJSON: row.AreaGeojson,
	}, nil
}

func (r *SpotsRepository) ListAmenities(ctx context.Context, spotID uuid.UUID) ([]spots.Amenity, error) {
	rows, err := r.queries.ListSpotAmenities(ctx, spotID)
	if err != nil {
		return nil, err
	}
	out := make([]spots.Amenity, 0, len(rows))
	for _, row := range rows {
		out = append(out, spots.Amenity{
			Type:      row.Type,
			DistanceM: int(row.DistanceM),
			Source:    row.Source,
		})
	}
	return out, nil
}

func (r *SpotsRepository) ListSources(ctx context.Context, spotID uuid.UUID) ([]spots.Source, error) {
	rows, err := r.queries.ListSpotSources(ctx, spotID)
	if err != nil {
		return nil, err
	}
	out := make([]spots.Source, 0, len(rows))
	for _, row := range rows {
		out = append(out, spots.Source{
			Name:       row.Source,
			ID:         row.SourceID,
			Version:    intPtr(row.Version),
			ImportedAt: row.ImportedAt,
		})
	}
	return out, nil
}

func (r *SpotsRepository) ListForecast(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]spots.ForecastPoint, error) {
	rows, err := r.queries.ListSpotForecast(ctx, ListSpotForecastParams{
		SpotID:   spotID,
		TsHour:   from,
		TsHour_2: to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]spots.ForecastPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, spots.ForecastPoint{
			TSHour: row.TsHour,
			Score:  int(row.Score),
			Components: spots.Components{
				Weather: int16Ptr(row.SWeather),
				Crowd:   int16Ptr(row.SCrowd),
				Amenity: int16Ptr(row.SAmenity),
				Shade:   int16Ptr(row.SShade),
				Fresh:   int16Ptr(row.SFresh),
			},
			CapReason:  textPtr(row.CapReason),
			Reasons:    reasonsFromJSON(row.Reasons),
			Confidence: int(row.Confidence),
		})
	}
	return out, nil
}

func scoreSnapshotFromRow(score pgtype.Int2, confidence pgtype.Int2, capReason pgtype.Text, reasons []byte, computedAt pgtype.Timestamptz) *spots.ScoreSnapshot {
	if !score.Valid && !confidence.Valid {
		return nil
	}
	return &spots.ScoreSnapshot{
		Score:      int16Ptr(score),
		Confidence: int16Ptr(confidence),
		CapReason:  textPtr(capReason),
		Reasons:    reasonsFromJSON(reasons),
		ComputedAt: timePtr(computedAt),
	}
}

func namesFromJSON(raw []byte) spots.Names {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func reasonsFromJSON(raw []byte) []spots.Reason {
	if len(raw) == 0 {
		return []spots.Reason{}
	}
	var r []spots.Reason
	if err := json.Unmarshal(raw, &r); err != nil {
		return []spots.Reason{}
	}
	return r
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func int16Ptr(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int16)
	return &i
}

func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int32)
	return &i
}

func uuidPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}
