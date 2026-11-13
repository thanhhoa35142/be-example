package holidays

import (
	"context"
	"testing"
	"time"
)

func dates(days []Day) map[string]string {
	m := map[string]string{}
	for _, d := range days {
		m[d.Date.Format("2006-01-02")] = d.Name
	}
	return m
}

func TestAllPackCountriesCovered(t *testing.T) {
	for _, c := range []string{"US", "AU", "GB", "DE", "FR", "ES", "IT", "NL", "BE", "LU", "AT", "CH", "DK", "SE", "IE", "CZ", "PL", "PT"} {
		for _, y := range []int{2026, 2027} {
			days, ok := For(c, y)
			if !ok || len(days) < 4 {
				t.Errorf("%s %d: covered=%v days=%d", c, y, ok, len(days))
			}
			seen := map[time.Time]bool{}
			for _, d := range days {
				if seen[d.Date] || d.Date.Year() != y || d.Name == "" {
					t.Errorf("%s %d: bad/duplicate day %+v", c, y, d)
				}
				seen[d.Date] = true
			}
		}
	}
	if _, ok := For("ZZ", 2026); ok || Covered("ZZ") {
		t.Fatal("ZZ must be uncovered")
	}
}

func TestGB2026(t *testing.T) {
	got := dates(mustFor(t, "GB", 2026))
	want := []string{"2026-01-01", "2026-04-03", "2026-04-06", "2026-05-04", "2026-05-25", "2026-08-31", "2026-12-25", "2026-12-28"}
	if len(got) != len(want) {
		t.Fatalf("GB 2026: %d days %v", len(got), got)
	}
	for _, d := range want {
		if _, ok := got[d]; !ok {
			t.Errorf("missing %s in %v", d, got)
		}
	}
}

// cal returns midnight in time.Local; a naive .UTC() would shift every date
// back a day on hosts east of UTC.
func TestNoDateShiftInEasternZone(t *testing.T) {
	old := time.Local
	defer func() { time.Local = old }()
	time.Local = time.FixedZone("UTC+7", 7*3600)
	got := dates(mustFor(t, "DE", 2026))
	for _, d := range []string{"2026-01-01", "2026-04-03", "2026-05-01", "2026-10-03", "2026-12-25"} {
		if _, ok := got[d]; !ok {
			t.Errorf("DE %s missing in UTC+7 host: %v", d, got)
		}
	}
	time.Local = time.FixedZone("UTC-9", -9*3600)
	if _, ok := dates(mustFor(t, "DE", 2026))["2026-10-03"]; !ok {
		t.Error("DE 2026-10-03 missing in UTC-9 host")
	}
}

func TestRegionalAndEveDaysExcluded(t *testing.T) {
	ch := dates(mustFor(t, "CH", 2026))
	for _, d := range []string{"2026-01-02", "2026-03-19", "2026-01-06"} {
		if _, ok := ch[d]; ok {
			t.Errorf("CH cantonal holiday %s must be excluded", d)
		}
	}
	if _, ok := ch["2026-08-01"]; !ok {
		t.Error("CH national day missing")
	}
	se := dates(mustFor(t, "SE", 2026))
	for _, d := range []string{"2026-12-24", "2026-12-31"} { // Julafton / Nyarsafton: ObservanceOther
		if _, ok := se[d]; ok {
			t.Errorf("SE eve %s must be excluded", d)
		}
	}
}

func TestAustraliaOnlyCommonDates(t *testing.T) {
	au := dates(mustFor(t, "AU", 2026))
	for _, d := range []string{"2026-01-01", "2026-01-26", "2026-04-03", "2026-12-25"} {
		if _, ok := au[d]; !ok {
			t.Errorf("AU %s should be national: %v", d, au)
		}
	}
	// King's Birthday / Labour Day differ per state.
	for _, d := range []string{"2026-06-08", "2026-10-05"} {
		if _, ok := au[d]; ok {
			t.Errorf("AU %s is state-specific", d)
		}
	}
}

func mustFor(t *testing.T, c string, y int) []Day {
	t.Helper()
	d, ok := For(c, y)
	if !ok {
		t.Fatalf("%s not covered", c)
	}
	return d
}

type fakeRepo struct {
	countries []string
	rows      []Row
}

func (f *fakeRepo) Countries(context.Context) ([]string, error) { return f.countries, nil }
func (f *fakeRepo) Upsert(_ context.Context, rows []Row) (int64, error) {
	f.rows = rows
	return int64(len(rows)), nil
}

func TestRefreshStats(t *testing.T) {
	repo := &fakeRepo{countries: []string{"NL", "ZZ"}}
	st, err := NewService(repo).Refresh(context.Background(), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if st.Countries != 1 || len(st.Uncovered) != 1 || st.Uncovered[0] != "ZZ" {
		t.Fatalf("stats: %+v", st)
	}
	if st.Rows == 0 || int(st.Rows) != st.PerCountry["NL"] {
		t.Fatalf("rows: %+v", st)
	}
	if len(st.Years) != 2 || st.Years[1] != 2027 {
		t.Fatalf("years: %v", st.Years)
	}
}
