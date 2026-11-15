package osm

import (
	"bufio"
	"os"
	"testing"
)

func BenchmarkOsmiumStream(b *testing.B) {
	path := os.Getenv("OSMIUM_BENCH_FILE")
	if path == "" {
		b.Skip("OSMIUM_BENCH_FILE not set")
	}

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
