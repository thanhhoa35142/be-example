package osm

import (
	"bufio"
	"os"
	"testing"
)

// BenchmarkOsmiumStream measures the geojsonseq stream + bbox prefilter on
// a real export (the dominant ingest stage for country extracts):
//
//	OSMIUM_BENCH_FILE=/path/netherlands-latest.f….export.geojsonseq \
//	  go test ./internal/modules/ingest/osm -bench OsmiumStream -benchtime 1x
func BenchmarkOsmiumStream(b *testing.B) {
	path := os.Getenv("OSMIUM_BENCH_FILE")
	if path == "" {
		b.Skip("OSMIUM_BENCH_FILE not set")
	}
	// Amsterdam-sized pack inside a country export.
	bounds := Bounds{West: 4.72, South: 52.27, East: 5.08, North: 52.43}
	for i := 0; i < b.N; i++ {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		snap := &osmiumSnapshot{pack: bounds, file: f, reader: bufio.NewReaderSize(f, 1<<20)}
		n := 0
		for _, err := range snap.Features() {
			if err != nil {
				b.Fatal(err)
			}
			n++
		}
		b.ReportMetric(float64(n), "features")
	}
}
