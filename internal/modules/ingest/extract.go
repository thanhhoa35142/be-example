package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Extract refresher downloads Geofabrik .pbf extracts so the (future)
// osmium adapter has a local snapshot to work from. osm_extract values are
// Geofabrik paths without the ".osm.pbf" suffix, e.g.
// "europe/netherlands-latest".
//
// Files land in dir as "<base>.osm.pbf"; the previous file is kept as
// "<base>.prev.osm.pbf" so a bad download never leaves zero snapshots.

const geofabrikBase = "https://download.geofabrik.de/"

// RefreshExtracts downloads each extract not fetched within maxAge, or all
// when force is true. Returns the extract keys actually downloaded.
func RefreshExtracts(ctx context.Context, client *http.Client, dir string, extracts []string, maxAge time.Duration, force bool) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var fetched []string
	for _, extract := range extracts {
		if extract == "" || strings.Contains(extract, "..") {
			return nil, fmt.Errorf("invalid extract key %q", extract)
		}
		base := filepath.Base(extract)
		dest := filepath.Join(dir, base+".osm.pbf")
		if !force {
			if st, err := os.Stat(dest); err == nil && time.Since(st.ModTime()) < maxAge {
				continue // fresh enough
			}
		}
		url := geofabrikBase + extract + ".osm.pbf"
		if err := download(ctx, client, url, dest); err != nil {
			return fetched, fmt.Errorf("extract %s: %w", extract, err)
		}
		fetched = append(fetched, extract)
	}
	return fetched, nil
}

func download(ctx context.Context, client *http.Client, url, dest string) error {
	tmp := dest + ".tmp"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// Keep the outgoing file as .prev before replacing (rename, not copy —
	// extracts can be several GB).
	prev := strings.TrimSuffix(dest, ".osm.pbf") + ".prev.osm.pbf"
	_ = os.Remove(prev)
	if _, err := os.Stat(dest); err == nil {
		_ = os.Rename(dest, prev)
	}
	return os.Rename(tmp, dest)
}
