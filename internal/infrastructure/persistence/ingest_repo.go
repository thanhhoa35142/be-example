package persistence

import (
	"context"
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
		if row.LastImportAt.Valid {
			t := row.LastImportAt.Time
			out[len(out)-1].LastImportAt = &t
		}
		if row.ExtractFetchedAt.Valid {
			t := row.ExtractFetchedAt.Time
			out[len(out)-1].ExtractFetchedAt = &t
		}
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
	if err := s.q.ClearIngestStaging(ctx, s.packID); err != nil {
		return err
	}
	return s.q.ClearIngestFeatures(ctx, s.packID)
}

// CopyRaw COPYs one batch of raw rows into t_ingest_staging.
func (s *ingestStore) CopyRaw(ctx context.Context, rows []ingest.RawRow) error {
	params := make([]CopyIngestStagingParams, len(rows))
	for i, r := range rows {
		params[i] = CopyIngestStagingParams{
			PackID:        s.packID,
			Source:        r.Source,
			SourceID:      r.SourceID,
			Role:          r.Role,
			Kind:          textOrNull(r.Kind),
			Amenity:       textOrNull(r.Amenity),
			Quality:       int2OrNull(r.Quality),
			ExcludeReason: textOrNull(r.ExcludeReason),
			Version:       int4OrNull(r.Version),
			Tags:          string(r.TagsJSON),
			Geojson:       string(r.GeoJSON),
			ContentHash:   r.ContentHash,
			RunID:         int8OrNull(r.RunID),
		}
	}
	_, err := s.q.CopyIngestStaging(ctx, params)
	return err
}

// MergeRaw moves staged rows into the raw layer (one row per source id,
// highest-dimension geometry wins) and empties the staging slice.
func (s *ingestStore) MergeRaw(ctx context.Context) (int64, error) {
	n, err := s.q.MergeIngestStaging(ctx, s.packID)
	if err != nil {
		return 0, err
	}
	return n, s.q.ClearIngestStaging(ctx, s.packID)
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

func (s *ingestStore) OrphanTableClusters(ctx context.Context, epsMerc float64, minPts int) ([]ingest.TableCluster, error) {
	rows, err := s.q.ClusterOrphanTables(ctx, ClusterOrphanTablesParams{
		PackID:            s.packID,
		StClusterdbscan:   epsMerc,
		StClusterdbscan_2: minPts,
	})
	if err != nil {
		return nil, err
	}
	// Rows arrive ordered by cluster_id — group consecutive runs.
	var out []ingest.TableCluster
	for _, r := range rows {
		cid := int64(f64(r.ClusterID))
		m := ingest.TableClusterMember{
			Source:   r.Source,
			SourceID: r.SourceID,
			Version:  int(r.Version.Int32),
			Lon:      f64(r.Lon),
			Lat:      f64(r.Lat),
		}
		if n := len(out); n > 0 && out[n-1].ID == cid {
			c := &out[n-1]
			c.Members = append(c.Members, m)
			c.Lon += m.Lon
			c.Lat += m.Lat
			c.Size++
			continue
		}
		out = append(out, ingest.TableCluster{
			ID:      cid,
			Members: []ingest.TableClusterMember{m},
			Lon:     m.Lon,
			Lat:     m.Lat,
			Size:    1,
		})
	}
	for i := range out {
		out[i].Lon /= float64(out[i].Size)
		out[i].Lat /= float64(out[i].Size)
	}
	return out, nil
}

func spotRowFrom(r ListPackSpotsRow) ingest.SpotRow {
	return ingest.SpotRow{
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
	}
}

func (s *ingestStore) Spots(ctx context.Context) ([]ingest.SpotRow, error) {
	rows, err := s.q.ListPackSpots(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.SpotRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, spotRowFrom(r))
	}
	return out, nil
}

// SpotsNear returns pending/active spots of the pack within radiusM meters
// of (lon, lat), nearest first — the spatial fallback for OSM remaps.
func (s *ingestStore) SpotsNear(ctx context.Context, lon, lat, radiusM float64) ([]ingest.SpotRow, error) {
	rows, err := s.q.ListSpotsNearPoint(ctx, ListSpotsNearPointParams{
		CityPackID:    s.packID,
		StMakepoint:   lon,
		StMakepoint_2: lat,
		StDwithin:     radiusM,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ingest.SpotRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, spotRowFrom(ListPackSpotsRow{
			ID:           r.ID,
			Status:       r.Status,
			Quality:      r.Quality,
			Curated:      r.Curated,
			MergedInto:   r.MergedInto,
			Kind:         r.Kind,
			Name:         r.Name,
			Names:        r.Names,
			Attrs:        r.Attrs,
			Country:      r.Country,
			Tz:           r.Tz,
			CellID:       r.CellID,
			StatusReason: r.StatusReason,
			Lon:          r.Lon,
			Lat:          r.Lat,
			AreaGeojson:  r.AreaGeojson,
		}))
	}
	return out, nil
}

// SourceLinks preloads provenance for the planner in one query.
func (s *ingestStore) SourceLinks(ctx context.Context) (map[[2]string]ingest.SourceLink, error) {
	rows, err := s.q.ListPackSourceLinks(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]string]ingest.SourceLink, len(rows))
	for _, r := range rows {
		out[[2]string{r.Source, r.SourceID}] = ingest.SourceLink{
			SpotID:      r.SpotID,
			PackID:      r.CityPackID,
			Version:     int(r.Version.Int32),
			Via:         r.Via,
			ContentHash: r.ContentHash.String,
			Found:       true,
		}
	}
	return out, nil
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
	cell := ns.CellID
	if cell == 0 {
		cell = s.cellID(ns.Lat, ns.Lon)
	}
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
		CellID:        cell,
	})
}

func (s *ingestStore) SetStatus(ctx context.Context, id uuid.UUID, status, reason string) error {
	return s.q.SetSpotStatus(ctx, SetSpotStatusParams{
		ID:           id,
		Status:       status,
		StatusReason: pgtype.Text{String: reason, Valid: reason != ""},
	})
}

// UpsertSources writes all queued provenance links in one statement.
func (s *ingestStore) UpsertSources(ctx context.Context, ups []ingest.SourceUpsert) error {
	if len(ups) == 0 {
		return nil
	}
	p := UpsertSpotSourcesParams{
		SpotIds:       make([]uuid.UUID, len(ups)),
		Sources:       make([]string, len(ups)),
		SourceIds:     make([]string, len(ups)),
		Versions:      make([]int32, len(ups)),
		Vias:          make([]string, len(ups)),
		ContentHashes: make([]string, len(ups)),
	}
	for i, u := range ups {
		p.SpotIds[i], p.Sources[i], p.SourceIds[i] = u.SpotID, u.Source, u.SourceID
		p.Versions[i], p.Vias[i], p.ContentHashes[i] = int32(u.Version), u.Via, u.ContentHash
	}
	return s.q.UpsertSpotSources(ctx, p)
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

func (s *ingestStore) Sourceless(ctx context.Context) ([]ingest.SourcelessSpot, error) {
	rows, err := s.q.ListSourcelessSpots(ctx, s.packID)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.SourcelessSpot, 0, len(rows))
	for _, r := range rows {
		out = append(out, ingest.SourcelessSpot{ID: r.ID, Status: r.Status})
	}
	return out, nil
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

// AssignAmenities attaches all of the pack's amenity features set-based.
func (s *ingestStore) AssignAmenities(ctx context.Context, radiusDeg, radiusM float64) (int64, error) {
	return s.q.AssignPackAmenities(ctx, AssignPackAmenitiesParams{
		PackID:    s.packID,
		RadiusDeg: radiusDeg,
		RadiusM:   radiusM,
	})
}

func (s *ingestStore) TouchImport(ctx context.Context) error {
	return s.q.TouchPackImport(ctx, s.packID)
}

func (s *ingestStore) NotifyIngestDone(ctx context.Context) error {
	return s.q.NotifyIngestDone(ctx)
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
