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

var ErrGuardTripped = errors.New("ingest guard tripped")

const fallbackRadiusM = 30.0

// Environment-fact thresholds (documented in INGEST_ENV_FACTS_PLAN.md).
const (
	// EnvWaterMaxM: nearest water farther than this is not recorded.
	EnvWaterMaxM = 500.0
	// EnvWatersideM: a spot is "waterside" when water is within this many
	// meters of its footprint (and the water is not a pond).
	EnvWatersideM = 50
	// EnvPondMaxM2: lake polygons under 0.2 ha are ponds.
	EnvPondMaxM2 = 2000.0
	// EnvCampParkCover: a park/beach is excluded as "inside a campsite"
	// when a campsite covers this share of it (and holds its anchor).
	EnvCampParkCover = 0.5
	// EnvCampNodeRadiusM: a camp site node excludes site/table candidates
	// within this distance.
	EnvCampNodeRadiusM = 40.0
)

// EnvKeys are the t_spots.attrs keys owned by the env step; it rewrites
// them on every run (stale values are removed).
var EnvKeys = []string{
	"waterDistanceM", "waterKind", "waterside", "treeCoverPct",
	"protected", "protectedFee", "protectedOperator",
}

type Stats struct {
	FeaturesRead     int            `json:"features_read"`
	ExcludedByReason map[string]int `json:"excluded_by_reason,omitempty"`
	Candidates       int            `json:"candidates"`
	Absorbed         int            `json:"absorbed"`
	Created          int            `json:"created"`
	Updated          int            `json:"updated"`
	Unchanged        int            `json:"unchanged"`
	DuplicateGeoms   int            `json:"duplicate_geoms,omitempty"`
	Activated        int            `json:"activated"`
	Pending          int            `json:"pending"`
	BelowFloor       int            `json:"below_floor"`
	Closed           int            `json:"closed"`
	HiddenVanished   int            `json:"hidden_vanished"`
	SkippedCurated   int            `json:"skipped_curated"`
	FallbackMatched  int            `json:"fallback_matched"`
	AmenitiesLinked  int            `json:"amenities_linked"`
	OrphanSites      int            `json:"orphan_sites"`
	ForeignOwned     int            `json:"foreign_owned"`
	// Environment layers (INGEST_ENV_FACTS_PLAN).
	Layers           map[string]int `json:"layers,omitempty"` // features read per layer_* reason
	CampsiteSkipped  int            `json:"campsite_skipped"` // candidates inside a campsite, excluded
	CampsiteHidden   int            `json:"campsite_hidden"`  // existing spots hidden because of it
	PolygonRecovered int            `json:"polygon_recovered"`
	PolygonKept      int            `json:"polygon_kept"` // update would have dropped an existing area
	WaterNear        int            `json:"water_near"`   // spots with water within EnvWaterMaxM
	Waterside        int            `json:"waterside"`    // water within EnvWatersideM, not a pond
	WithTreeCover    int            `json:"with_tree_cover"`
	ProtectedSpots   int            `json:"protected_spots"`
	GuardTripped     bool           `json:"guard_tripped,omitempty"`
	GuardForced      bool           `json:"guard_forced,omitempty"`

	StageMs map[string]int64 `json:"stage_ms,omitempty"`
}

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

type CandidateRow struct {
	Source         string
	SourceID       string
	Kind           string
	Quality        int
	Version        int
	TagsJSON       []byte
	ContentHash    string
	GeoJSON        string
	Lon            float64
	Lat            float64
	PolygonGeoJSON *string
	AreaHa         float64
}

type AbsorbedRow struct {
	Source         string
	SourceID       string
	ParentSource   string
	ParentSourceID string
}

type TableClusterMember struct {
	Source   string
	SourceID string
	Version  int
	Lon      float64
	Lat      float64
}

type TableCluster struct {
	ID      int64
	Members []TableClusterMember
	Lon     float64
	Lat     float64
	Size    int
}

type SourceLink struct {
	SpotID      uuid.UUID
	PackID      uuid.UUID
	Version     int
	Via         string
	ContentHash string
	Found       bool
}

type SourcelessSpot struct {
	ID     uuid.UUID
	Status string
}

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

type NewSpot struct {
	Kind         string
	Name         string
	NamesJSON    []byte
	AttrsJSON    []byte
	Lon          float64
	Lat          float64
	AreaGeoJSON  *string
	Country      string
	TZ           string
	CellID       int64
	Status       string
	Quality      int
	StatusReason string
}

type SourceUpsert struct {
	SpotID      uuid.UUID
	Source      string
	SourceID    string
	Version     int
	Via         string
	ContentHash string
}

// LayerPrep is the outcome of validating the environment layers and
// excluding campsite candidates (source keys are [source, source_id]).
type LayerPrep struct {
	CampsiteExcluded [][2]string
}

// EnvStats counts active/pending spots of the pack per environment fact.
type EnvStats struct {
	WaterNear, Waterside, WithTreeCover, Protected int
}

type Store interface {
	ClearRaw(ctx context.Context) error

	CopyRaw(ctx context.Context, rows []RawRow) error

	MergeRaw(ctx context.Context) (int64, error)

	AnalyzeRaw(ctx context.Context) error
	Candidates(ctx context.Context) ([]CandidateRow, error)
	Absorbed(ctx context.Context) ([]AbsorbedRow, error)

	AmenityTypes(ctx context.Context) (map[[2]string]int, error)

	PrepareLayers(ctx context.Context) (LayerPrep, error)

	UpdateEnvAttrs(ctx context.Context, radiusDeg float64) (EnvStats, error)

	OrphanTableClusters(ctx context.Context, epsMerc float64, minPts int) ([]TableCluster, error)
	Spots(ctx context.Context) ([]SpotRow, error)

	SpotsNear(ctx context.Context, lon, lat, radiusM float64) ([]SpotRow, error)

	SourceLinks(ctx context.Context) (map[[2]string]SourceLink, error)
	InsertSpot(ctx context.Context, s NewSpot) (uuid.UUID, error)
	UpdateSpot(ctx context.Context, id uuid.UUID, s NewSpot) error
	SetStatus(ctx context.Context, id uuid.UUID, status, reason string) error

	UpsertSources(ctx context.Context, ups []SourceUpsert) error
	ResetSightings(ctx context.Context) error
	BumpMissing(ctx context.Context) error
	MissingActive(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error)
	MissingPending(ctx context.Context, minMissingRuns int) ([]uuid.UUID, error)

	Sourceless(ctx context.Context) ([]SourcelessSpot, error)
	RecentConfirm(ctx context.Context, spotID uuid.UUID, days int) (bool, error)
	ClearAmenities(ctx context.Context) error

	AssignAmenities(ctx context.Context, radiusDeg, radiusM float64) (int64, error)
	TouchImport(ctx context.Context) error

	NotifyIngestDone(ctx context.Context) error
}

type Runner interface {
	WithTx(ctx context.Context, packID uuid.UUID, dryRun bool, fn func(Store) error) error
}

type PackLister interface {
	ListIngestPacks(ctx context.Context) ([]osm.Pack, error)
}

type Service struct {
	Src   osm.FeatureSource
	Store Runner
	Clf   osm.Classifier
	Pol   osm.Policy

	AmenityRadiusM float64

	ForceGuard bool
}

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

func (s *Service) RunPack(ctx context.Context, pack osm.Pack, runID int64, dryRun bool) (Stats, error) {
	var st Stats
	snap, err := s.Src.Open(ctx, pack)
	if err != nil {
		return st, fmt.Errorf("open snapshot: %w", err)
	}
	defer func() { _ = snap.Close() }()

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
			if c.Role == osm.RoleExcluded && strings.HasPrefix(c.Exclude, "layer_") {
				if st.Layers == nil {
					st.Layers = map[string]int{}
				}
				st.Layers[c.Exclude]++
			} else if c.Role == osm.RoleExcluded {
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

		if err := store.AnalyzeRaw(ctx); err != nil {
			return fmt.Errorf("analyze raw: %w", err)
		}
		mark("merge")

		prep, err := store.PrepareLayers(ctx)
		if err != nil {
			return fmt.Errorf("prepare layers: %w", err)
		}
		st.CampsiteSkipped = len(prep.CampsiteExcluded)
		mark("layers")

		absorbed, err := store.Absorbed(ctx)
		if err != nil {
			return err
		}
		absorbedSet := map[[2]string][2]string{}
		childCount := map[[2]string]int{}
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
		amenityTypes, err := store.AmenityTypes(ctx)
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

		// Campsite candidates were excluded from the raw layer; spots that
		// already exist for them would otherwise stay active forever (an
		// excluded row never resets sightings, and "missing" would take two
		// runs and count against the close guard).
		for _, k := range prep.CampsiteExcluded {
			l := links[k]
			if !l.Found || l.PackID != pack.ID || l.Via != "primary" {
				continue
			}
			sp, ok := spotRow[l.SpotID]
			if !ok || locked(sp) {
				continue
			}
			if sp.Status != string(osm.StatusActive) && sp.Status != string(osm.StatusPending) {
				continue
			}
			if err := store.SetStatus(ctx, sp.ID, string(osm.StatusHidden), osm.ReasonInsideCampsite); err != nil {
				return err
			}
			if sp.Status == string(osm.StatusActive) {
				activeCount--
			}
			sp.Status = string(osm.StatusHidden)
			spotRow[sp.ID] = sp
			st.CampsiteHidden++
		}

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
			switch {
			case ns.AreaGeoJSON == nil && sp.AreaGeoJSON != nil:
				// A point-only candidate (an Overpass-collapsed relation, or
				// a dedup that kept the point) must never wipe a polygon
				// that an earlier run assembled: keep area and anchor.
				ns.AreaGeoJSON = sp.AreaGeoJSON
				ns.Lon, ns.Lat = sp.Lon, sp.Lat
				st.PolygonKept++
			case ns.AreaGeoJSON != nil && sp.AreaGeoJSON == nil:
				st.PolygonRecovered++
			}
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

		parentSpot := map[[2]string]uuid.UUID{}
		type childLink struct {
			up        SourceUpsert
			parentKey [2]string
		}
		var lateChildren []childLink

		for _, c := range cands {
			key := [2]string{c.Source, c.SourceID}
			parentKey, isAbsorbed := absorbedSet[key]

			if c.Kind == "table" && !isAbsorbed {
				continue
			}

			quality := c.Quality + qp.Bonus(c.AreaHa, amenityTypes[key])
			if childCount[key] > 0 {
				quality += qp.TableEvidence
			}
			quality = max(0, min(quality, 100))

			var tags map[string]string
			_ = json.Unmarshal(c.TagsJSON, &tags)
			name := tags["name"]

			link := links[key]
			oldVersion, oldHash := link.Version, link.ContentHash

			if link.Found && link.Via == "absorbed" && !isAbsorbed {
				link.Found = false
			}
			if link.Found && link.PackID != pack.ID {

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

		for _, l := range lateChildren {
			id, ok := parentSpot[l.parentKey]
			if !ok {
				continue
			}
			l.up.SpotID = id
			queue(l.up)
			st.Absorbed++
		}

		eps := pol.OrphanClusterEpsM
		if eps <= 0 {
			eps = osm.DefaultPolicy.OrphanClusterEpsM
		}
		cosLat := packCosLat(pack)
		clusters, err := store.OrphanTableClusters(ctx, eps/cosLat, pol.MinTablesForSite)
		if err != nil {
			return err
		}
		claimed := map[uuid.UUID]bool{}
		for _, cl := range clusters {

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
					continue
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

		if err := store.ResetSightings(ctx); err != nil {
			return err
		}
		if err := store.BumpMissing(ctx); err != nil {
			return err
		}

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
				continue
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

		if src, ok := s.Src.(osm.EnvLayerSource); ok && src.EnvLayers() {
			env, err := store.UpdateEnvAttrs(ctx, EnvWaterMaxM/(111320*cosLat))
			if err != nil {
				return fmt.Errorf("env attrs: %w", err)
			}
			st.WaterNear, st.Waterside = env.WaterNear, env.Waterside
			st.WithTreeCover, st.ProtectedSpots = env.WithTreeCover, env.Protected
			mark("env")
		}

		if err := store.TouchImport(ctx); err != nil {
			return err
		}
		return store.NotifyIngestDone(ctx)
	})
	return st, err
}

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

func sameSpot(sp SpotRow, ns NewSpot) bool {
	const eps = 1e-7
	return sp.Quality == ns.Quality && sp.Name == ns.Name &&
		sameTagAttrs(sp.AttrsJSON, ns.AttrsJSON) &&
		(ns.CellID == 0 || sp.CellID == ns.CellID) &&
		math.Abs(sp.Lon-ns.Lon) < eps && math.Abs(sp.Lat-ns.Lat) < eps &&
		(sp.AreaGeoJSON != nil) == (ns.AreaGeoJSON != nil)
}

func packCosLat(pack osm.Pack) float64 {
	centerLat := (pack.Bounds.South + pack.Bounds.North) / 2
	return math.Max(math.Cos(centerLat*math.Pi/180), 0.2)
}

func attrsJSON(tags map[string]string) []byte {
	attrs := map[string]string{}
	for _, k := range osm.AttrKeys {
		if v, ok := tags[k]; ok {
			attrs[k] = v
		}
	}
	b, _ := json.Marshal(attrs)
	return b
}

// sameTagAttrs compares only the tag-derived attr keys: t_spots.attrs also
// holds derived env facts that the planner does not own.
func sameTagAttrs(have, want []byte) bool {
	var h, w map[string]any
	_ = json.Unmarshal(have, &h)
	_ = json.Unmarshal(want, &w)
	for _, k := range osm.AttrKeys {
		hv, hok := h[k]
		wv, wok := w[k]
		if hok != wok || hv != wv {
			return false
		}
	}
	return true
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
