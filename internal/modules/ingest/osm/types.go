// Package osm implements spot ingestion from OpenStreetMap per
// docs/INGEST_STRATEGY.md: Geofabrik .pbf → osmium → GeoJSON stream →
// classify → raw layer (t_ingest_features) → PostGIS enrich → pure Go
// plan → apply in one transaction per city pack.
//
// This file defines the ports and shared types; the osmium/Overpass
// adapters, planner SQL and worker jobs land in later commits.
package osm

import (
	"context"
	"iter"
	"time"

	"github.com/google/uuid"
)

// RawFeature is one classified OSM object streamed from a FeatureSource.
type RawFeature struct {
	Source   string // "osm" (future: "overpass", "user", ...)
	SourceID string // "n123" | "w456" | "r789"
	Version  int
	Tags     map[string]string
	GeoJSON  []byte // Point | Polygon | MultiPolygon, EPSG:4326
}

// Pack is the ingest unit: one city pack = one boundary + one extract.
type Pack struct {
	ID         uuid.UUID
	Slug       string
	Country    string
	TZ         string
	OSMExtract string // Geofabrik extract key, e.g. "netherlands-latest"
}

// Snapshot is an opened extract: features stream once, then Close.
type Snapshot interface {
	AsOf() time.Time
	Features() iter.Seq2[RawFeature, error]
	Close() error
}

// FeatureSource opens a snapshot for a pack. Implementations:
// osmiumPBF (prod), overpass (dev/fallback), fixture (tests).
type FeatureSource interface {
	Open(ctx context.Context, p Pack) (Snapshot, error)
}
