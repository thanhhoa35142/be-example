// Package ingest orchestrates the OSM → t_spots pipeline described in
// docs/INGEST_STRATEGY.md: a FeatureSource streams raw OSM objects, the
// pure Classifier tags them, everything lands in the raw layer
// (t_ingest_features), then a pure planner decides creates/updates/closes
// against canonical t_spots — all inside one transaction per city pack.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
)

// ErrGuardTripped aborts a pack run when a guardrail trips (e.g. the run
// would close more than MaxCloseFraction of the pack's active spots —
// usually a truncated extract or broken filter).
var ErrGuardTripped = errors.New("ingest guard tripped")

// fallbackRadiusM bounds the spatial+name fallback: when an OSM object is
// remapped to a new id, the new candidate may re-link to an existing
// pending/active spot of the same kind and name within this distance.
const fallbackRadiusM = 30.0

// Stats is written to t_job_runs.stats after each pack run.
type Stats struct {
	FeaturesRead     int            `json:"features_read"`
	ExcludedByReason map[string]int `json:"excluded_by_reason,omitempty"`
	Candidates       int            `json:"candidates"`
	Absorbed         int            `json:"absorbed"`
	Created          int            `json:"created"`
	Updated          int            `json:"updated"`
	Unchanged        int            `json:"unchanged"`
	DuplicateGeoms   int            `json:"duplicate_geoms,omitempty"` // line+area twins collapsed in the raw merge
	Activated        int            `json:"activated"`
	Pending          int            `json:"pending"`
	BelowFloor       int            `json:"below_floor"`
	Closed           int            `json:"closed"`
	HiddenVanished   int            `json:"hidden_vanished"`
	SkippedCurated   int            `json:"skipped_curated"` // curated/merged/admin-hidden/closed-by-report: data never touched
	FallbackMatched  int            `json:"fallback_matched"`
	AmenitiesLinked  int            `json:"amenities_linked"` // (spot, type) rows written
	OrphanSites      int            `json:"orphan_sites"`
	ForeignOwned     int            `json:"foreign_owned"`
	GuardTripped     bool           `json:"guard_tripped,omitempty"`
	GuardForced      bool           `json:"guard_forced,omitempty"`
	// StageMs is wall time per pipeline stage (stream_copy, merge, load,
	// plan, upsert_sources, lifecycle, amenities) — the first place to look
	// when a pack gets slow.
	StageMs map[string]int64 `json:"stage_ms,omitempty"`
}

// RawRow is one classified feature destined for t_ingest_features.
type RawRow struct {
	Source        string
	SourceID      string
	Version       int
	Role          string
	Kind          string
	Amenity       string
	Quality       int
	ExcludeReason string
	TagsJSON      []byte
	GeoJSON       []byte
	ContentHash   string
	RunID         int64
}

// CandidateRow mirrors ListCandidateFeatures.
type CandidateRow struct {
	Source         string
	SourceID       string
	Kind           string
	Quality        int
	Version        int
	TagsJSON       []byte
	ContentHash    string
	GeoJSON        string // ST_AsGeoJSON of the feature geometry
	Lon            float64
	Lat            float64
	PolygonGeoJSON *string // non-nil when the geometry is an area
}

// AbsorbedRow mirrors ListAbsorbedCandidates.
type AbsorbedRow struct {
	Source         string
	SourceID       string
	ParentSource   string
	ParentSourceID string
}

// TableClusterMember is one orphan table inside a DBSCAN cluster.
type TableClusterMember struct {
	Source   string
	SourceID string
	Version  int
	Lon      float64
	Lat      float64
}

// TableCluster groups orphan tables that form one candidate picnic site.
type TableCluster struct {
	ID      int64 // DBSCAN cluster id (only unique within this run)
	Members []TableClusterMember
	Lon     float64 // member centroid
	Lat     float64
	Size    int
}

// SourceLink is the provenance record for one (source, source_id).
type SourceLink struct {
	SpotID      uuid.UUID
	PackID      uuid.UUID // owning pack — differs on boundary straddlers
	Version     int
	Via         string // "primary" | "absorbed"
	ContentHash string
	Found       bool
}

// SourcelessSpot is an active/pending spot with zero primary sources.
type SourcelessSpot struct {
	ID     uuid.UUID
	Status string
}

// SpotRow mirrors ListPackSpots.
type SpotRow struct {
	ID           uuid.UUID
	Status       string
	Quality      int
	Curated      bool
	Kind         string
	Name         string
	NamesJSON    []byte
	AttrsJSON    []byte
	Country      string
	TZ           string
	CellID       int64
	Lon          float64
	Lat          float64
	AreaGeoJSON  *string
	StatusReason string
}

// NewSpot carries the fields needed to insert or refresh a t_spots row.
type NewSpot struct {
	Kind         string
	Name         string
	NamesJSON    []byte
	AttrsJSON    []byte
	Lon          float64
	Lat          float64
	AreaGeoJSON  *string // nil → point-only spot
	Country      string
	TZ           string
	CellID       int64 // geo cell of (Lat, Lon); 0 → store computes it
	Status       string
	Quality      int
	StatusReason string
}

// SourceUpsert is one queued t_spot_sources write (flushed in one batch).
type SourceUpsert struct {
	SpotID      uuid.UUID
	Source      string
	SourceID    string
	Version     int
	Via         string // "primary" | "absorbed"
	ContentHash string
}

// Store is the tx-scoped persistence port the planner writes through.
// Implemented by internal/infrastructure/persistence.
type Store interface {
	ClearRaw(ctx context.Context) error
	// CopyRaw bulk-loads a batch of raw rows into staging (COPY).
	CopyRaw(ctx context.Context, rows []RawRow) error
	// MergeRaw moves staged rows into the raw layer, keeping one row per
	// (source, source_id) — the highest-dimension geometry wins — and
	// returns the number of raw rows kept.
	MergeRaw(ctx context.Context) (int64, error)
	Candidates(ctx context.Context) ([]CandidateRow, error)
	Absorbed(ctx context.Context) ([]AbsorbedRow, error)
	// OrphanTableClusters returns DBSCAN clusters of table-kind candidates
	// not absorbed into a park/beach. epsMerc is epsilon in Web Mercator
	// meters (caller adjusts for latitude); minPts is minpoints.
	OrphanTableClusters(ctx context.Context, epsMerc float64, minPts int) ([]TableCluster, error)
	Spots(ctx context.Context) ([]SpotRow, error)
	// SpotsNear returns pending/active spots within radiusM meters of
	// (lon, lat), nearest first — used by the name+proximity fallback when
	// SpotBySource misses (OSM object remaps).
	SpotsNear(ctx context.Context, lon, lat, radiusM float64) ([]SpotRow, error)
	// SourceLinks preloads provenance: every source of this pack's spots
	// plus foreign-owned links of this run's candidates.
	SourceLinks(ctx context.Context) (map[[2]string]SourceLink, error)
	InsertSpot(ctx context.Context, s NewSpot) (uuid.UUID, error)
	UpdateSpot(ctx context.Context, id uuid.UUID, s NewSpot) error
	SetStatus(ctx context.Context, id uuid.UUID, status, reason string) error
	// UpsertSources links sources to spots in one statement. via is
	// "primary" (the candidate that IS the spot) or "absorbed" (child
	// evidence); only primary sources keep a spot alive.
	UpsertSources(ctx context.Context, ups []SourceUpsert) error
	ResetSightings(ctx context.Context) error
	BumpMissing(ctx context.Context) error
	MissingActive(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error)
	MissingPending(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error)
	// Sourceless returns active/pending spots with no primary source —
	// their identity moved elsewhere and they must be closed out.
	Sourceless(ctx context.Context) ([]SourcelessSpot, error)
	RecentConfirm(ctx context.Context, spotID uuid.UUID, days int) (bool, error)
	ClearAmenities(ctx context.Context) error
	// AssignAmenities attaches every amenity feature to its nearest
	// active/pending spot footprint (set-based); returns rows written.
	AssignAmenities(ctx context.Context, radiusDeg, radiusM float64) (int64, error)
	TouchImport(ctx context.Context) error
	// NotifyIngestDone emits pg_notify('pickhour_ingest') inside the tx —
	// delivered only on commit, so dry-runs and rollbacks stay silent.
	NotifyIngestDone(ctx context.Context) error
}

// Runner executes fn inside one DB transaction scoped to a pack;
// dryRun rolls back instead of committing but still returns fn's error
// (stats are collected either way).
type Runner interface {
	WithTx(ctx context.Context, packID uuid.UUID, dryRun bool, fn func(Store) error) error
}

// PackLister enumerates packs for the scheduled job.
type PackLister interface {
	ListIngestPacks(ctx context.Context) ([]osm.Pack, error)
}

// Service runs the ingest pipeline for one pack.
type Service struct {
	Src   osm.FeatureSource
	Store Runner
	Clf   osm.Classifier
	Pol   osm.Policy
	// AmenityRadiusM bounds how far an amenity feature may sit from a spot
	// anchor/area and still count (PostGIS does the real distance check).
	AmenityRadiusM float64
	// ForceGuard lets an operator push through a run the blast-radius guard
	// would abort (e.g. a real mass removal confirmed by hand).
	ForceGuard bool
}

// rawBatchSize bounds the in-memory COPY buffer while streaming features.
const rawBatchSize = 5000

func (s *Service) policy() osm.Policy {
	if s.Pol == (osm.Policy{}) {
		return osm.DefaultPolicy
	}
	return s.Pol
}

func (s *Service) classifier() osm.Classifier {
	if s.Clf.Policy == (osm.QualityPolicy{}) {
		return osm.Classifier{Policy: osm.DefaultQualityPolicy}
	}
	return s.Clf
}

// contentHash fingerprints the canonical inputs that should trigger a
// spot update: kind + name + sorted tags + normalized geometry text.
func contentHash(c osm.Classified, f osm.RawFeature) string {
	h := sha256.New()
	h.Write([]byte(c.Kind))
	h.Write([]byte{0})
	h.Write([]byte(c.Name))
	h.Write([]byte{0})
	tags, _ := json.Marshal(f.Tags)
	h.Write(tags)
	h.Write([]byte{0})
	h.Write(f.GeoJSON)
	return hex.EncodeToString(h.Sum(nil))
}

// RunPack executes the full pipeline for one pack inside a single
// transaction. The heavy FeatureSource I/O happens before the tx opens.
//
// Round-trips are bounded: the raw layer is COPY'd in batches, provenance
// is preloaded once, source links are flushed in one batched upsert, and
// amenities are assigned set-based. Only spot inserts, real spot updates
// (unchanged spots are skipped) and the name+proximity fallback for
// unmatched candidates remain per-row.
func (s *Service) RunPack(ctx context.Context, pack osm.Pack, runID int64, dryRun bool) (Stats, error) {
	var st Stats
	snap, err := s.Src.Open(ctx, pack)
	if err != nil {
		return st, fmt.Errorf("open snapshot: %w", err)
	}
	defer snap.Close()

	st.StageMs = map[string]int64{}
	stageStart := time.Now()
	mark := func(stage string) {
		now := time.Now()
		st.StageMs[stage] = now.Sub(stageStart).Milliseconds()
		stageStart = now
	}
	err = s.Store.WithTx(ctx, pack.ID, dryRun, func(store Store) error {
		if err := store.ClearRaw(ctx); err != nil {
			return err
		}
		// Stage 1: stream → classify → COPY into staging in bounded batches
		// (the feature set never materializes in memory) → set-based merge.
		clf := s.classifier()
		batch := make([]RawRow, 0, rawBatchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := store.CopyRaw(ctx, batch)
			batch = batch[:0]
			return err
		}
		for f, err := range snap.Features() {
			if err != nil {
				return fmt.Errorf("feature stream: %w", err)
			}
			st.FeaturesRead++
			c := clf.Classify(f)
			if c.Role == osm.RoleExcluded {
				if st.ExcludedByReason == nil {
					st.ExcludedByReason = map[string]int{}
				}
				st.ExcludedByReason[c.Exclude]++
			}
			tagsJSON, _ := json.Marshal(f.Tags)
			batch = append(batch, RawRow{
				Source: f.Source, SourceID: f.SourceID, Version: f.Version,
				Role: string(c.Role), Kind: c.Kind, Amenity: string(c.Amenity),
				Quality: c.Quality, ExcludeReason: c.Exclude,
				TagsJSON: tagsJSON, GeoJSON: f.GeoJSON,
				ContentHash: contentHash(c, f), RunID: runID,
			})
			if len(batch) >= rawBatchSize {
				if err := flush(); err != nil {
					return fmt.Errorf("copy raw: %w", err)
				}
			}
		}
		if err := flush(); err != nil {
			return fmt.Errorf("copy raw: %w", err)
		}
		mark("stream_copy")
		kept, err := store.MergeRaw(ctx)
		if err != nil {
			return fmt.Errorf("merge raw: %w", err)
		}
		st.DuplicateGeoms = st.FeaturesRead - int(kept)
		if pe, ok := snap.(interface{ ParseErrors() int }); ok && pe.ParseErrors() > 0 {
			if st.ExcludedByReason == nil {
				st.ExcludedByReason = map[string]int{}
			}
			st.ExcludedByReason["parse_error"] = pe.ParseErrors()
		}

		mark("merge")

		// Stage 2: load planner inputs once.
		absorbed, err := store.Absorbed(ctx)
		if err != nil {
			return err
		}
		absorbedSet := map[[2]string][2]string{} // child key → parent key
		childCount := map[[2]string]int{}        // parent key → absorbed children
		for _, a := range absorbed {
			absorbedSet[[2]string{a.Source, a.SourceID}] = [2]string{a.ParentSource, a.ParentSourceID}
			childCount[[2]string{a.ParentSource, a.ParentSourceID}]++
		}
		cands, err := store.Candidates(ctx)
		if err != nil {
			return err
		}
		st.Candidates = len(cands)
		spots, err := store.Spots(ctx)
		if err != nil {
			return err
		}
		links, err := store.SourceLinks(ctx)
		if err != nil {
			return err
		}
		pol := s.policy()
		qp := osm.DefaultQualityPolicy
		if s.Clf.Policy != (osm.QualityPolicy{}) {
			qp = s.Clf.Policy
		}

		mark("load")

		spotRow := map[uuid.UUID]SpotRow{}
		activeCount := 0
		for _, sp := range spots {
			spotRow[sp.ID] = sp
			if sp.Status == string(osm.StatusActive) {
				activeCount++
			}
		}

		// Queued provenance writes, deduped by key (last write wins) and
		// mirrored into links so later lookups in this run see them.
		var ups []SourceUpsert
		upIdx := map[[2]string]int{}
		queue := func(u SourceUpsert) {
			k := [2]string{u.Source, u.SourceID}
			if i, ok := upIdx[k]; ok {
				ups[i] = u
			} else {
				upIdx[k] = len(ups)
				ups = append(ups, u)
			}
			links[k] = SourceLink{SpotID: u.SpotID, PackID: pack.ID, Version: u.Version,
				Via: u.Via, ContentHash: u.ContentHash, Found: true}
		}

		// apply decides and writes the canonical row for an existing spot.
		// Returns false when the spot is locked (curated, merged,
		// admin-hidden, closed by report): its data is never touched.
		apply := func(spotID uuid.UUID, ns NewSpot, sourceChanged bool) error {
			sp := spotRow[spotID]
			if locked(sp) {
				st.SkippedCurated++
				return nil
			}
			newStatus, reason := pol.NextStatus(osm.Evidence{
				CurrentStatus:  osm.Status(sp.Status),
				Quality:        ns.Quality,
				Trusted:        true,
				SourceChanged:  sourceChanged,
				ClosedByReport: sp.StatusReason == osm.ReasonClosedByReport,
				StatusReason:   sp.StatusReason,
				SourcePresent:  true,
			})
			ns.Status = string(newStatus)
			if cell, err := geo.CellIDFromLatLng(ns.Lat, ns.Lon); err == nil {
				ns.CellID = cell
			}
			if !sourceChanged && ns.Status == sp.Status && sameSpot(sp, ns) {
				st.Unchanged++
				return nil
			}
			if err := store.UpdateSpot(ctx, spotID, ns); err != nil {
				return err
			}
			if ns.Status != sp.Status {
				if err := store.SetStatus(ctx, spotID, ns.Status, reason); err != nil {
					return fmt.Errorf("status %s→%s for %s: %w", sp.Status, ns.Status, spotID, err)
				}
				if ns.Status == string(osm.StatusActive) {
					st.Activated++
				}
			}
			st.Updated++
			return nil
		}
		create := func(ns NewSpot) (uuid.UUID, bool, error) {
			status, reason := pol.NewSpotStatus(osm.Evidence{Quality: ns.Quality, Trusted: true})
			if status == "" {
				st.BelowFloor++
				return uuid.UUID{}, false, nil
			}
			ns.Status, ns.StatusReason = string(status), reason
			id, err := store.InsertSpot(ctx, ns)
			if err != nil {
				return id, false, err
			}
			if status == osm.StatusActive {
				st.Activated++
			} else {
				st.Pending++
			}
			st.Created++
			return id, true, nil
		}

		parentSpot := map[[2]string]uuid.UUID{} // candidate key → spot id
		type childLink struct {
			up        SourceUpsert
			parentKey [2]string
		}
		var lateChildren []childLink

		for _, c := range cands {
			key := [2]string{c.Source, c.SourceID}
			parentKey, isAbsorbed := absorbedSet[key]

			// Unabsorbed tables only become spots via orphan clustering.
			if c.Kind == "table" && !isAbsorbed {
				continue
			}

			quality := c.Quality
			if childCount[key] > 0 {
				quality = min(quality+qp.TableEvidence, 100)
			}

			var tags map[string]string
			_ = json.Unmarshal(c.TagsJSON, &tags)
			name := tags["name"]

			link := links[key]
			oldVersion, oldHash := link.Version, link.ContentHash
			// A source still linked as 'absorbed' while its candidate is no
			// longer absorbed became its own spot — treat as unmatched.
			if link.Found && link.Via == "absorbed" && !isAbsorbed {
				link.Found = false
			}
			if link.Found && link.PackID != pack.ID {
				// Boundary straddler owned by another pack.
				st.ForeignOwned++
				continue
			}

			if isAbsorbed {
				up := SourceUpsert{Source: c.Source, SourceID: c.SourceID, Version: c.Version,
					Via: "absorbed", ContentHash: c.ContentHash}
				if id, ok := parentSpot[parentKey]; ok {
					up.SpotID = id
					queue(up)
					st.Absorbed++
				} else {
					lateChildren = append(lateChildren, childLink{up, parentKey})
				}
				continue
			}

			var spotID uuid.UUID
			found, viaFallback := false, false
			if link.Found {
				if _, ok := spotRow[link.SpotID]; ok {
					spotID, found = link.SpotID, true
				}
			}
			if !found {
				if norm := normalizeName(name); norm != "" {
					// OSM remap fallback: same kind + name within
					// fallbackRadiusM, unique match only.
					near, err := store.SpotsNear(ctx, c.Lon, c.Lat, fallbackRadiusM)
					if err != nil {
						return err
					}
					var match uuid.UUID
					matches := 0
					for _, sp := range near {
						if sp.Kind == c.Kind && normalizeName(sp.Name) == norm {
							match, matches = sp.ID, matches+1
						}
					}
					if matches == 1 {
						spotID, found, viaFallback = match, true, true
						st.FallbackMatched++
					}
				}
			}

			ns := NewSpot{
				Kind: c.Kind, Name: name,
				NamesJSON: namesJSON(tags), AttrsJSON: attrsJSON(tags),
				Lon: c.Lon, Lat: c.Lat, AreaGeoJSON: c.PolygonGeoJSON,
				Country: pack.Country, TZ: pack.TZ, Quality: quality,
			}
			if found {
				changed := viaFallback || c.Version > oldVersion ||
					(c.ContentHash != "" && c.ContentHash != oldHash)
				if err := apply(spotID, ns, changed); err != nil {
					return err
				}
			} else {
				id, ok, err := create(ns)
				if err != nil {
					return fmt.Errorf("insert spot %s/%s: %w", c.Source, c.SourceID, err)
				}
				if !ok {
					continue
				}
				spotID = id
			}
			queue(SourceUpsert{SpotID: spotID, Source: c.Source, SourceID: c.SourceID,
				Version: c.Version, Via: "primary", ContentHash: c.ContentHash})
			parentSpot[key] = spotID
		}

		// Absorbed children whose parents resolved after them.
		for _, l := range lateChildren {
			id, ok := parentSpot[l.parentKey]
			if !ok {
				continue // parent below floor / foreign — child stays raw-only
			}
			l.up.SpotID = id
			queue(l.up)
			st.Absorbed++
		}

		// Stage 2b: orphan table clusters → synthetic kind=site spots.
		// DBSCAN runs in Web Mercator; eps is stretched by 1/cos(lat).
		eps := pol.OrphanClusterEpsM
		if eps <= 0 {
			eps = osm.DefaultPolicy.OrphanClusterEpsM
		}
		cosLat := packCosLat(pack)
		clusters, err := store.OrphanTableClusters(ctx, eps/cosLat, pol.MinTablesForSite)
		if err != nil {
			return err
		}
		claimed := map[uuid.UUID]bool{} // one cluster per existing site spot
		for _, cl := range clusters {
			// Drop members owned by another pack BEFORE deciding, otherwise
			// a spot is created with no linkable source and closed again by
			// the sourceless sweep — a junk row per run.
			own := cl.Members[:0:0]
			for _, m := range cl.Members {
				if l := links[[2]string{m.Source, m.SourceID}]; l.Found && l.PackID != pack.ID {
					st.ForeignOwned++
					continue
				}
				own = append(own, m)
			}
			if len(own) < pol.MinTablesForSite {
				continue
			}
			var lon, lat float64
			for _, m := range own {
				lon += m.Lon
				lat += m.Lat
			}
			lon /= float64(len(own))
			lat /= float64(len(own))
			quality := min(qp.TableEvidence+10*len(own), 100)

			// Re-find via any member's provenance (kind=site only, and each
			// spot only once — a split cluster must not map both halves to
			// the same spot).
			var spotID uuid.UUID
			found := false
			for _, m := range own {
				l := links[[2]string{m.Source, m.SourceID}]
				if !l.Found || claimed[l.SpotID] {
					continue
				}
				if row, ok := spotRow[l.SpotID]; ok && row.Kind == "site" {
					spotID, found = l.SpotID, true
					break
				}
			}

			ns := NewSpot{
				Kind: "site", Lon: lon, Lat: lat,
				NamesJSON: []byte("{}"), AttrsJSON: []byte("{}"),
				Country: pack.Country, TZ: pack.TZ, Quality: quality,
			}
			if found {
				if err := apply(spotID, ns, false); err != nil {
					return err
				}
			} else {
				id, ok, err := create(ns)
				if err != nil {
					return fmt.Errorf("insert table-cluster spot: %w", err)
				}
				if !ok {
					continue // below floor — members stay raw-only
				}
				spotID = id
			}
			claimed[spotID] = true
			st.OrphanSites++
			for _, m := range own {
				queue(SourceUpsert{SpotID: spotID, Source: m.Source, SourceID: m.SourceID,
					Version: m.Version, Via: "primary"})
			}
		}

		mark("plan")
		if err := store.UpsertSources(ctx, ups); err != nil {
			return fmt.Errorf("upsert sources: %w", err)
		}
		mark("upsert_sources")

		// Stage 3: sightings → missing runs → guarded closes.
		if err := store.ResetSightings(ctx); err != nil {
			return err
		}
		if err := store.BumpMissing(ctx); err != nil {
			return err
		}
		// Blast-radius guard, evaluated on what is absent THIS run (not only
		// on spots past CloseAfterMissing) so a truncated extract aborts on
		// its first run, before raw layer/amenities are replaced. Sourceless
		// closes count too.
		absent, err := store.MissingActive(ctx, 1)
		if err != nil {
			return err
		}
		sourceless, err := store.Sourceless(ctx)
		if err != nil {
			return err
		}
		destructive := len(absent)
		for _, sp := range sourceless {
			if sp.Status == string(osm.StatusActive) {
				destructive++
			}
		}
		if activeCount > 0 && float64(destructive) > pol.MaxCloseFraction*float64(activeCount) {
			if !s.ForceGuard {
				st.GuardTripped = true
				st.Closed = destructive
				return fmt.Errorf("%w: %d of %d active spots absent or sourceless", ErrGuardTripped, destructive, activeCount)
			}
			st.GuardForced = true
		}
		missingActive, err := store.MissingActive(ctx, pol.CloseAfterMissing)
		if err != nil {
			return err
		}
		for _, id := range missingActive {
			if ok, err := store.RecentConfirm(ctx, id, pol.ConfirmShieldDays); err != nil {
				return err
			} else if ok {
				continue // shielded — stays active
			}
			if err := store.SetStatus(ctx, id, string(osm.StatusClosed), osm.ReasonSourceMissing); err != nil {
				return err
			}
			st.Closed++
		}
		missingPending, err := store.MissingPending(ctx, pol.CloseAfterMissing)
		if err != nil {
			return err
		}
		for _, id := range missingPending {
			if err := store.SetStatus(ctx, id, string(osm.StatusHidden), osm.ReasonVanished); err != nil {
				return err
			}
			st.HiddenVanished++
		}
		// Spots whose primary sources all moved to other spots this run
		// have no identity left — close/hide them.
		for _, sp := range sourceless {
			next := osm.StatusHidden
			if sp.Status == string(osm.StatusActive) {
				next = osm.StatusClosed
			}
			if err := store.SetStatus(ctx, sp.ID, string(next), osm.ReasonSourcesMoved); err != nil {
				return err
			}
			if next == osm.StatusClosed {
				st.Closed++
			} else {
				st.HiddenVanished++
			}
		}

		mark("lifecycle")

		// Stage 4: amenities, set-based.
		if err := store.ClearAmenities(ctx); err != nil {
			return err
		}
		radius := s.AmenityRadiusM
		if radius <= 0 {
			radius = 300
		}
		n, err := store.AssignAmenities(ctx, radius/(111320*cosLat), radius)
		if err != nil {
			return fmt.Errorf("assign amenities: %w", err)
		}
		st.AmenitiesLinked = int(n)
		mark("amenities")

		if err := store.TouchImport(ctx); err != nil {
			return err
		}
		return store.NotifyIngestDone(ctx)
	})
	return st, err
}

// locked reports spots the importer must never modify (INGEST_STRATEGY D4):
// curated, merged, hidden by an admin, closed by an accepted report.
func locked(sp SpotRow) bool {
	if sp.Curated {
		return true
	}
	switch osm.Status(sp.Status) {
	case osm.StatusMerged:
		return true
	case osm.StatusHidden:
		return !osm.ImporterHiddenReason(sp.StatusReason)
	case osm.StatusClosed:
		return sp.StatusReason == osm.ReasonClosedByReport
	}
	return false
}

// sameSpot reports whether writing ns would leave the canonical row as is
// (lets unchanged spots skip the UPDATE entirely).
func sameSpot(sp SpotRow, ns NewSpot) bool {
	const eps = 1e-7
	return sp.Quality == ns.Quality && sp.Name == ns.Name &&
		(ns.CellID == 0 || sp.CellID == ns.CellID) &&
		math.Abs(sp.Lon-ns.Lon) < eps && math.Abs(sp.Lat-ns.Lat) < eps &&
		(sp.AreaGeoJSON != nil) == (ns.AreaGeoJSON != nil)
}

func packCosLat(pack osm.Pack) float64 {
	centerLat := (pack.Bounds.South + pack.Bounds.North) / 2
	return math.Max(math.Cos(centerLat*math.Pi/180), 0.2) // polar packs: cap the stretch
}

var attrKeys = []string{"access", "fee", "reservation", "nudism", "supervised", "capacity", "description", "wikidata", "website"}

func attrsJSON(tags map[string]string) []byte {
	attrs := map[string]string{}
	for _, k := range attrKeys {
		if v, ok := tags[k]; ok {
			attrs[k] = v
		}
	}
	b, _ := json.Marshal(attrs)
	return b
}

func namesJSON(tags map[string]string) []byte {
	names := map[string]string{}
	for k, v := range tags {
		if lang, ok := strings.CutPrefix(k, "name:"); ok && lang != "" {
			names[lang] = v
		}
	}
	b, _ := json.Marshal(names)
	return b
}
