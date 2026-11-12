package persistence

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
)

// IngestRepo is the pool-level entry point: it lists packs and opens the
// per-pack transaction the ingest.Service planner writes through.
type IngestRepo struct {
	pool *pgxpool.Pool
}

func NewIngestRepo(pool *pgxpool.Pool) *IngestRepo {
	return &IngestRepo{pool: pool}
}

func (r *IngestRepo) ListIngestPacks(ctx context.Context) ([]osm.Pack, error) {
	rows, err := New(r.pool).ListIngestPacks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]osm.Pack, 0, len(rows))
	for _, row := range rows {
		out = append(out, osm.Pack{
			ID:         row.ID,
			Slug:       row.Slug,
			Country:    row.Country,
			TZ:         row.Tz,
			OSMExtract: row.OsmExtract.String,
		})
	}
	return out, nil
}

// DistinctExtracts lists osm_extract values used by any pack.
func (r *IngestRepo) DistinctExtracts(ctx context.Context) ([]string, error) {
	rows, err := New(r.pool).ListDistinctExtracts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, e := range rows {
		if e.Valid {
			out = append(out, e.String)
		}
	}
	return out, nil
}

// MarkExtractFetched stamps extract_fetched_at on all packs using extract.
func (r *IngestRepo) MarkExtractFetched(ctx context.Context, extract string, at time.Time) error {
	return New(r.pool).MarkExtractFetched(ctx, MarkExtractFetchedParams{
		OsmExtract:       pgtype.Text{String: extract, Valid: true},
		ExtractFetchedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
}

// PackBounds returns the pack boundary bbox for bbox-based sources.
func (r *IngestRepo) PackBounds(ctx context.Context, id uuid.UUID) (osm.Bounds, error) {
	row, err := New(r.pool).PackBBox(ctx, id)
	if err != nil {
		return osm.Bounds{}, err
	}
	return osm.Bounds{
		West:  f64(row.StXmin),
		South: f64(row.StYmin),
		East:  f64(row.StXmax),
		North: f64(row.StYmax),
	}, nil
}

// WithTx implements ingest.Runner.
func (r *IngestRepo) WithTx(ctx context.Context, packID uuid.UUID, dryRun bool, fn func(ingest.Store) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // no-op after Commit

	store := &ingestStore{q: New(tx), packID: packID}
	if err := fn(store); err != nil {
		return err
	}
	if dryRun {
		return tx.Rollback(ctx)
	}
	return tx.Commit(ctx)
}

// ingestStore is the tx-scoped Store for one pack.
type ingestStore struct {
	q      *Queries
	packID uuid.UUID
}

// NewIngestStoreTx is used by tests that already hold a tx.
func NewIngestStoreTx(tx pgx.Tx, packID uuid.UUID) ingest.Store {
	return &ingestStore{q: New(tx), packID: packID}
}

func (s *ingestStore) ClearRaw(ctx context.Context) error {
	return s.q.ClearIngestFeatures(ctx, s.packID)
}

func (s *ingestStore) InsertRaw(ctx context.Context, r ingest.RawRow) error {
	return s.q.InsertIngestFeature(ctx, InsertIngestFeatureParams{
		PackID:            s.packID,
		Source:            r.Source,
		SourceID:          r.SourceID,
		Role:              r.Role,
		Kind:              textOrNull(r.Kind),
		Amenity:           textOrNull(r.Amenity),
		Quality:           int2OrNull(r.Quality),
		ExcludeReason:     textOrNull(r.ExcludeReason),
		Version:           int4OrNull(r.Version),
		Column10:          r.TagsJSON,
		StGeomfromgeojson: string(r.GeoJSON),
		ContentHash:       r.ContentHash,
		RunID:             int8OrNull(r.RunID),
	})
}

func (s *ingestStore) Candidates(ctx context.Context) ([]ingest.CandidateRow, error) {
	rows, err := s.q.ListCandidateFeatures(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.CandidateRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ingest.CandidateRow{
			Source:         r.Source,
			SourceID:       r.SourceID,
			Kind:           r.Kind.String,
			Quality:        int(r.Quality.Int16),
			Version:        int(r.Version.Int32),
			TagsJSON:       r.Tags,
			ContentHash:    r.ContentHash,
			GeoJSON:        strOf(r.Geojson),
			Lon:            f64(r.Lon),
			Lat:            f64(r.Lat),
			PolygonGeoJSON: strPtr(r.PolygonGeojson),
		})
	}
	return out, nil
}

func (s *ingestStore) Absorbed(ctx context.Context) ([]ingest.AbsorbedRow, error) {
	rows, err := s.q.ListAbsorbedCandidates(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.AbsorbedRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ingest.AbsorbedRow{
			Source: r.Source, SourceID: r.SourceID,
			ParentSource: r.ParentSource, ParentSourceID: r.ParentSourceID,
		})
	}
	return out, nil
}

func (s *ingestStore) Amenities(ctx context.Context) ([]ingest.AmenityRow, error) {
	rows, err := s.q.ListAmenityFeatures(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.AmenityRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ingest.AmenityRow{
			Amenity: r.Amenity.String,
			Lon:     f64(r.Lon),
			Lat:     f64(r.Lat),
		})
	}
	return out, nil
}

func (s *ingestStore) Spots(ctx context.Context) ([]ingest.SpotRow, error) {
	rows, err := s.q.ListPackSpots(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.SpotRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ingest.SpotRow{
			ID:           r.ID,
			Status:       r.Status,
			Quality:      int(r.Quality.Int16),
			Curated:      r.Curated,
			Kind:         r.Kind,
			Name:         r.Name.String,
			NamesJSON:    r.Names,
			AttrsJSON:    r.Attrs,
			Country:      r.Country,
			TZ:           r.Tz,
			CellID:       r.CellID,
			Lon:          f64(r.Lon),
			Lat:          f64(r.Lat),
			AreaGeoJSON:  strPtr(r.AreaGeojson),
			StatusReason: r.StatusReason.String,
		})
	}
	return out, nil
}

func (s *ingestStore) SpotBySource(ctx context.Context, source, sourceID string) (uuid.UUID, int, bool, error) {
	row, err := s.q.FindSpotBySource(ctx, FindSpotBySourceParams{Source: source, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, false, nil
	}
	if err != nil {
		return uuid.Nil, 0, false, err
	}
	return row.SpotID, int(row.Version.Int32), true, nil
}

func (s *ingestStore) cellID(lat, lon float64) int64 {
	id, err := geo.CellIDFromLatLng(lat, lon)
	if err != nil {
		return 0
	}
	return id
}

func (s *ingestStore) InsertSpot(ctx context.Context, ns ingest.NewSpot) (uuid.UUID, error) {
	var area pgtype.Text
	if ns.AreaGeoJSON != nil {
		area = pgtype.Text{String: *ns.AreaGeoJSON, Valid: true}
	}
	return s.q.InsertIngestSpot(ctx, InsertIngestSpotParams{
		CityPackID:    s.packID,
		Kind:          ns.Kind,
		Name:          textOrNull(ns.Name),
		Column4:       ns.NamesJSON,
		StMakepoint:   ns.Lon,
		StMakepoint_2: ns.Lat,
		Country:       ns.Country,
		Tz:            ns.TZ,
		CellID:        s.cellID(ns.Lat, ns.Lon),
		Status:        ns.Status,
		Quality:       int2OrNull(ns.Quality),
		Column12:      ns.AttrsJSON,
		StatusReason:  textOrNull(ns.StatusReason),
		Area:          area,
	})
}

func (s *ingestStore) UpdateSpot(ctx context.Context, id uuid.UUID, ns ingest.NewSpot) error {
	var area pgtype.Text
	if ns.AreaGeoJSON != nil {
		area = pgtype.Text{String: *ns.AreaGeoJSON, Valid: true}
	}
	return s.q.UpdateIngestSpot(ctx, UpdateIngestSpotParams{
		ID:            id,
		Name:          textOrNull(ns.Name),
		Column3:       ns.NamesJSON,
		Column4:       ns.AttrsJSON,
		Quality:       int2OrNull(ns.Quality),
		StMakepoint:   ns.Lon,
		StMakepoint_2: ns.Lat,
		Area:          area,
	})
}

func (s *ingestStore) SetStatus(ctx context.Context, id uuid.UUID, status, reason string) error {
	return s.q.SetSpotStatus(ctx, SetSpotStatusParams{
		ID:           id,
		Status:       status,
		StatusReason: pgtype.Text{String: reason, Valid: reason != ""},
	})
}

func (s *ingestStore) UpsertSource(ctx context.Context, spotID uuid.UUID, source, sourceID string, version int) error {
	return s.q.UpsertSpotSource(ctx, UpsertSpotSourceParams{
		SpotID:   spotID,
		Source:   source,
		SourceID: sourceID,
		Version:  int4OrNull(version),
	})
}

func (s *ingestStore) ResetSightings(ctx context.Context) error {
	return s.q.ResetSourceSightings(ctx, s.packID)
}

func (s *ingestStore) BumpMissing(ctx context.Context) error {
	_, err := s.q.BumpMissingSources(ctx, s.packID)
	return err
}

func (s *ingestStore) MissingActive(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error) {
	return s.q.ListSpotsMissingAllSources(ctx, ListSpotsMissingAllSourcesParams{
		CityPackID:  s.packID,
		MissingRuns: int16(minMissingRuns),
	})
}

func (s *ingestStore) MissingPending(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error) {
	return s.q.ListPendingVanished(ctx, ListPendingVanishedParams{
		CityPackID:  s.packID,
		MissingRuns: int16(minMissingRuns),
	})
}

func (s *ingestStore) RecentConfirm(ctx context.Context, spotID uuid.UUID, days int) (bool, error) {
	n, err := s.q.CountRecentSpotConfirms(ctx, CountRecentSpotConfirmsParams{
		SpotID:  spotID,
		Column2: pgtype.Text{String: strconv.Itoa(days), Valid: true},
	})
	return n > 0, err
}

func (s *ingestStore) ClearAmenities(ctx context.Context) error {
	return s.q.ClearPackAmenities(ctx, s.packID)
}

// AssignAmenity links one amenity feature to the nearest active spot whose
// area contains it within radius meters; no-op when nothing is near.
func (s *ingestStore) AssignAmenity(ctx context.Context, lat, lon float64, amenity string) error {
	row, err := s.q.NearestSpotForPoint(ctx, NearestSpotForPointParams{
		StMakepoint:   lon,
		StMakepoint_2: lat,
		CityPackID:    s.packID,
		StDwithin:     300,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	dist := int16(f64(row.DistM) + 0.5)
	if dist < 0 {
		dist = 0
	}
	return s.q.UpsertSpotAmenity(ctx, UpsertSpotAmenityParams{
		SpotID:    row.ID,
		Type:      amenity,
		DistanceM: dist,
	})
}

func (s *ingestStore) TouchImport(ctx context.Context) error {
	return s.q.TouchPackImport(ctx, s.packID)
}

// pgtype helpers — sqlc generates nullable columns as pgtype.*.
func textOrNull(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func int2OrNull(v int) pgtype.Int2 {
	if v == 0 {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(v), Valid: true}
}

func int4OrNull(v int) pgtype.Int4 {
	if v == 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(v), Valid: true}
}

func int8OrNull(v int64) pgtype.Int8 {
	if v == 0 {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: v, Valid: true}
}

// f64/strOf/strPtr convert interface{} columns (ST_X/ST_Y/ST_AsGeoJSON and
// CASE expressions sqlc can't type) into concrete Go values.
func f64(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case int:
		return float64(t)
	case []byte:
		f, _ := strconv.ParseFloat(string(t), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func strOf(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	}
	return ""
}

func strPtr(v interface{}) *string {
	s := strOf(v)
	if s == "" {
		return nil
	}
	return &s
}

var _ = fmt.Sprintf // keep fmt import if unused
