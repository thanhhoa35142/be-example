package ingest

import (
	"context"
	"errors"
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
// when force is true. onFetched (optional) runs right after each download —
// the worker uses it to stamp extract_fetched_at, build the osmium
// artifacts and delete the raw .pbf before the next download, so peak disk
// is one raw extract. A failing extract does not stop the others; errors
// are joined. Returns the extract keys actually downloaded.
func RefreshExtracts(ctx context.Context, client *http.Client, dir string, extracts []string, maxAge time.Duration, force bool, onFetched func(extract string) error) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var fetched []string
	var errs []error
	for _, extract := range extracts {
		if ctx.Err() != nil {
			break
		}
		if extract == "" || strings.Contains(extract, "..") {
			errs = append(errs, fmt.Errorf("invalid extract key %q", extract))
			continue
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
			errs = append(errs, fmt.Errorf("extract %s: %w", extract, err))
			continue
		}
		fetched = append(fetched, extract)
		if onFetched != nil {
			if err := onFetched(extract); err != nil {
				errs = append(errs, fmt.Errorf("extract %s post-fetch: %w", extract, err))
			}
		}
	}
	return fetched, errors.Join(append(errs, ctx.Err())...)
}

// EnsureExtract downloads one extract when it is missing from dir (no
// freshness check) — used right before an ingest run so a pack added
// mid-cycle still finds its data. Reports whether a download happened so
// the caller can stamp extract_fetched_at.
func EnsureExtract(ctx context.Context, client *http.Client, dir, extract string) (bool, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if extract == "" || strings.Contains(extract, "..") {
		return false, fmt.Errorf("invalid extract key %q", extract)
	}
	dest := filepath.Join(dir, filepath.Base(extract)+".osm.pbf")
	if _, err := os.Stat(dest); err == nil {
		return false, nil
	}
	if err := download(ctx, client, geofabrikBase+extract+".osm.pbf", dest); err != nil {
		return false, fmt.Errorf("extract %s: %w", extract, err)
	}
	return true, nil
}

// RemoveExtract deletes the raw .pbf (and .prev) for an extract once its
// osmium artifacts are built. The artifacts (filtered pbf + geojsonseq,
// ~20% of the raw size) serve every ingest run until the next
// extract.refresh downloads a newer raw file.
func RemoveExtract(dir, extract string) {
	if extract == "" || strings.Contains(extract, "..") {
		return
	}
	base := filepath.Base(extract)
	for _, suffix := range []string{".osm.pbf", ".prev.osm.pbf"} {
		_ = os.Remove(filepath.Join(dir, base+suffix))
	}
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
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// Reject truncated downloads: a partial .pbf would poison the osmium
	// artifact cache (it only checks file mtime/size > 0).
	if resp.ContentLength > 0 && n != resp.ContentLength {
		os.Remove(tmp)
		return fmt.Errorf("truncated download: got %d of %d bytes", n, resp.ContentLength)
	}
	if n == 0 {
		os.Remove(tmp)
		return fmt.Errorf("empty download")
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
