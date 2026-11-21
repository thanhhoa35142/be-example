package osm

import (
	"context"
	"iter"
	"time"

	"github.com/google/uuid"
)

type RawFeature struct {
	Source   string
	SourceID string
	Version  int
	Tags     map[string]string
	GeoJSON  []byte
}

type Pack struct {
	ID         uuid.UUID
	Slug       string
	Country    string
	TZ         string
	OSMExtract string

	LastImportAt     *time.Time
	ExtractFetchedAt *time.Time

	Bounds Bounds
}

type Bounds struct {
	West, South, East, North float64
}

type Snapshot interface {
	AsOf() time.Time
	Features() iter.Seq2[RawFeature, error]
	Close() error
}

type FeatureSource interface {
	Open(ctx context.Context, p Pack) (Snapshot, error)
}

// EnvLayerSource is implemented by feature sources that deliver the
// environment layers (water, wood, campsite, protected area). The ingest
// only recomputes the derived env attrs for such sources.
type EnvLayerSource interface {
	EnvLayers() bool
}
