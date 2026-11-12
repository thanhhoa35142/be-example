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
	"strings"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/ingest/osm"
)

// ErrGuardTripped aborts a pack run when a guardrail trips (e.g. the run
// would close more than MaxCloseFraction of the pack's active spots —
// usually a truncated extract or broken filter).
var ErrGuardTripped = errors.New("ingest guard tripped")

// Stats is written to t_job_runs.stats after each pack run.
type Stats struct {
	FeaturesRead     int            `json:"features_read"`
	ExcludedByReason map[string]int `json:"excluded_by_reason,omitempty"`
	Candidates       int            `json:"candidates"`
	Absorbed         int            `json:"absorbed"`
	Created          int            `json:"created"`
	Updated          int            `json:"updated"`
	Activated        int            `json:"activated"`
	Pending          int            `json:"pending"`
	BelowFloor       int            `json:"below_floor"`
	Closed           int            `json:"closed"`
	HiddenVanished   int            `json:"hidden_vanished"`
	SkippedCurated   int            `json:"skipped_curated"`
	AmenitiesLinked  int            `json:"amenities_linked"`
	GuardTripped     bool           `json:"guard_tripped,omitempty"`
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

// AmenityRow mirrors ListAmenityFeatures.
type AmenityRow struct {
	Amenity string
	Lon     float64
	Lat     float64
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
	Status       string
	Quality      int
	StatusReason string
}

// Store is the tx-scoped persistence port the planner writes through.
// Implemented by internal/infrastructure/persistence.
type Store interface {
	ClearRaw(ctx context.Context) error
	InsertRaw(ctx context.Context, r RawRow) error
	Candidates(ctx context.Context) ([]CandidateRow, error)
	Absorbed(ctx context.Context) ([]AbsorbedRow, error)
	Amenities(ctx context.Context) ([]AmenityRow, error)
	Spots(ctx context.Context) ([]SpotRow, error)
	SpotBySource(ctx context.Context, source, sourceID string) (spotID uuid.UUID, version int, found bool, err error)
	InsertSpot(ctx context.Context, s NewSpot) (uuid.UUID, error)
	UpdateSpot(ctx context.Context, id uuid.UUID, s NewSpot) error
	SetStatus(ctx context.Context, id uuid.UUID, status, reason string) error
	UpsertSource(ctx context.Context, spotID uuid.UUID, source, sourceID string, version int) error
	ResetSightings(ctx context.Context) error
	BumpMissing(ctx context.Context) error
	MissingActive(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error)
	MissingPending(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error)
	RecentConfirm(ctx context.Context, spotID uuid.UUID, days int) (bool, error)
	ClearAmenities(ctx context.Context) error
	AssignAmenity(ctx context.Context, lat, lon float64, amenity string) error
	TouchImport(ctx context.Context) error
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
}

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
func (s *Service) RunPack(ctx context.Context, pack osm.Pack, runID int64, dryRun bool) (Stats, error) {
	var st Stats
	snap, err := s.Src.Open(ctx, pack)
	if err != nil {
		return st, fmt.Errorf("open snapshot: %w", err)
	}
	defer snap.Close()

	err = s.Store.WithTx(ctx, pack.ID, dryRun, func(store Store) error {
		if err := store.ClearRaw(ctx); err != nil {
			return err
		}
		// Stage 1: stream → classify → raw layer.
		clf := s.classifier()
		var rows []RawRow
		for f, err := range snap.Features() {
			if err != nil {
				return fmt.Errorf("feature stream: %w", err)
			}
			st.FeaturesRead++
			c := clf.Classify(f)
			switch c.Role {
			case osm.RoleExcluded:
				if st.ExcludedByReason == nil {
					st.ExcludedByReason = map[string]int{}
				}
				st.ExcludedByReason[c.Exclude]++
			case osm.RoleCandidate:
				st.Candidates++
			case osm.RoleAmenity:
			}
			tagsJSON, _ := json.Marshal(f.Tags)
			rows = append(rows, RawRow{
				Source: f.Source, SourceID: f.SourceID, Version: f.Version,
				Role: string(c.Role), Kind: c.Kind, Amenity: string(c.Amenity),
				Quality: c.Quality, ExcludeReason: c.Exclude,
				TagsJSON: tagsJSON, GeoJSON: f.GeoJSON,
				ContentHash: contentHash(c, f), RunID: runID,
			})
		}
		for _, r := range rows {
			if err := store.InsertRaw(ctx, r); err != nil {
				return fmt.Errorf("insert raw %s/%s: %w", r.Source, r.SourceID, err)
			}
		}

		// Stage 2: load planner inputs.
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
		spots, err := store.Spots(ctx)
		if err != nil {
			return err
		}
		pol := s.policy()
		qp := osm.DefaultQualityPolicy
		if s.Clf.Policy != (osm.QualityPolicy{}) {
			qp = s.Clf.Policy
		}

		// Index existing spots by their osm source ids.
		spotBySource := map[[2]string]uuid.UUID{}
		spotRow := map[uuid.UUID]SpotRow{}
		for _, sp := range spots {
			spotRow[sp.ID] = sp
		}

		// parentSpot resolves a candidate key to a spot id.
		parentSpot := map[[2]string]uuid.UUID{}
		type childLink struct {
			source, sourceID string
			version          int
			parentKey        [2]string
		}
		var links []childLink

		activeCount := 0
		for _, sp := range spots {
			if sp.Status == string(osm.StatusActive) {
				activeCount++
			}
		}

		for _, c := range cands {
			key := [2]string{c.Source, c.SourceID}
			parentKey, isAbsorbed := absorbedSet[key]

			// Tables only become spots via orphan clustering (TODO); absorbed
			// tables/sites attach to their parent below.
			if c.Kind == "table" && !isAbsorbed {
				continue
			}

			// Quality: tag score + table-evidence bonus for parents.
			quality := c.Quality
			if n := childCount[key]; n > 0 {
				quality += qp.TableEvidence
				if quality > 100 {
					quality = 100
				}
			}

			// Resolve existing spot via provenance.
			var spotID uuid.UUID
			var oldVersion int
			found := false
			if id, ver, ok, err := store.SpotBySource(ctx, c.Source, c.SourceID); err != nil {
				return err
			} else if ok {
				spotID, oldVersion, found = id, ver, true
				spotBySource[key] = id
			}

			if isAbsorbed {
				// Attach this child's source to the parent's spot — resolved
				// in the second pass once parents have spot ids.
				if id, ok := parentSpot[parentKey]; ok {
					if err := store.UpsertSource(ctx, id, c.Source, c.SourceID, c.Version); err != nil {
						return err
					}
					st.Absorbed++
				} else {
					links = append(links, childLink{c.Source, c.SourceID, c.Version, parentKey})
				}
				continue
			}

			var area *string
			if c.PolygonGeoJSON != nil {
				area = c.PolygonGeoJSON
			}
			namesJSON, _ := json.Marshal(map[string]string{})
			var attrsJSON []byte = []byte("{}")
			var name string
			var tags map[string]string
			_ = json.Unmarshal(c.TagsJSON, &tags)
			name = tags["name"]
			attrs := map[string]string{}
			for _, k := range []string{"access", "fee", "reservation", "nudism", "supervised", "capacity", "description", "wikidata", "website"} {
				if v, ok := tags[k]; ok {
					attrs[k] = v
				}
			}
			if len(attrs) > 0 {
				attrsJSON, _ = json.Marshal(attrs)
			}
			names := map[string]string{}
			for k, v := range tags {
				if len(k) > 5 && k[:5] == "name:" {
					names[k[5:]] = v
				}
			}
			if len(names) > 0 {
				namesJSON, _ = json.Marshal(names)
			}

			ns := NewSpot{
				Kind: c.Kind, Name: name, NamesJSON: namesJSON, AttrsJSON: attrsJSON,
				Lon: c.Lon, Lat: c.Lat, AreaGeoJSON: area,
				Country: pack.Country, TZ: pack.TZ,
				Quality: quality,
			}

			if found {
				sp := spotRow[spotID]
				ns.Status = sp.Status
				ns.StatusReason = sp.StatusReason
				if sp.Curated {
					st.SkippedCurated++
				} else {
					newStatus, reason := pol.NextStatus(osm.Evidence{
						CurrentStatus:  osm.Status(sp.Status),
						Quality:        quality,
						Trusted:        true,
						SourceChanged:  c.Version > oldVersion,
						ClosedByReport: strings.Contains(sp.StatusReason, "report"),
					})
					ns.Status = string(newStatus)
					ns.StatusReason = reason
				}
				if err := store.UpdateSpot(ctx, spotID, ns); err != nil {
					return err
				}
				if !sp.Curated && ns.Status != sp.Status {
					if err := store.SetStatus(ctx, spotID, ns.Status, ns.StatusReason); err != nil {
						return err
					}
					if ns.Status == string(osm.StatusActive) && sp.Status != string(osm.StatusActive) {
						st.Activated++
					}
				}
				st.Updated++
			} else {
				status, reason := pol.NewSpotStatus(osm.Evidence{
					Quality: quality, Trusted: true,
				})
				if status == "" {
					st.BelowFloor++
					continue
				}
				ns.Status = string(status)
				ns.StatusReason = reason
				id, err := store.InsertSpot(ctx, ns)
				if err != nil {
					return fmt.Errorf("insert spot %s/%s: %w", c.Source, c.SourceID, err)
				}
				spotID = id
				if status == osm.StatusActive {
					st.Activated++
				} else {
					st.Pending++
				}
				st.Created++
			}
			if err := store.UpsertSource(ctx, spotID, c.Source, c.SourceID, c.Version); err != nil {
				return err
			}
			parentSpot[key] = spotID
		}

		// Second pass: attach absorbed children whose parents resolved late.
		for _, l := range links {
			id, ok := parentSpot[l.parentKey]
			if !ok {
				continue // parent below floor — child stays raw-only
			}
			if err := store.UpsertSource(ctx, id, l.source, l.sourceID, l.version); err != nil {
				return err
			}
			st.Absorbed++
		}

		// Stage 3: sightings → missing runs → guarded closes.
		if err := store.ResetSightings(ctx); err != nil {
			return err
		}
		if err := store.BumpMissing(ctx); err != nil {
			return err
		}
		missingActive, err := store.MissingActive(ctx, pol.CloseAfterMissing)
		if err != nil {
			return err
		}
		if activeCount > 0 && float64(len(missingActive)) > pol.MaxCloseFraction*float64(activeCount) {
			st.GuardTripped = true
			st.Closed = len(missingActive)
			return fmt.Errorf("%w: would close %d of %d active spots", ErrGuardTripped, len(missingActive), activeCount)
		}
		for _, id := range missingActive {
			if ok, err := store.RecentConfirm(ctx, id, pol.ConfirmShieldDays); err != nil {
				return err
			} else if ok {
				continue // shielded — logged via stats omission, stays active
			}
			if err := store.SetStatus(ctx, id, string(osm.StatusClosed), "source_missing"); err != nil {
				return err
			}
			st.Closed++
		}
		missingPending, err := store.MissingPending(ctx, pol.CloseAfterMissing)
		if err != nil {
			return err
		}
		for _, id := range missingPending {
			if err := store.SetStatus(ctx, id, string(osm.StatusHidden), "vanished_before_review"); err != nil {
				return err
			}
			st.HiddenVanished++
		}

		// Stage 4: amenities attach to the nearest active spot.
		if err := store.ClearAmenities(ctx); err != nil {
			return err
		}
		amenities, err := store.Amenities(ctx)
		if err != nil {
			return err
		}
		radius := s.AmenityRadiusM
		if radius <= 0 {
			radius = 300
		}
		for _, a := range amenities {
			if err := store.AssignAmenity(ctx, a.Lat, a.Lon, a.Amenity); err != nil {
				return err
			}
			st.AmenitiesLinked++
		}

		return store.TouchImport(ctx)
	})
	return st, err
}
