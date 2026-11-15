package spots

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type listRepo struct {
	Repository
	tsHour, from, to time.Time
	limit            int
	total            int
	rows             int
}

func (r *listRepo) List(_ context.Context, _ ListQuery, ts, from, to time.Time, limit int) ([]SpotWithScore, int, error) {
	r.tsHour, r.from, r.to, r.limit = ts, from, to, limit
	return make([]SpotWithScore, r.rows), r.total, nil
}

func TestSpotCap(t *testing.T) {
	box := func(span float64) BBox { return BBox{West: 0, South: 0, East: span, North: span / 2} }
	if got := SpotCap(box(0.12), 24, true); got != 0 {
		t.Fatalf("span at threshold must be uncapped, got %d", got)
	}
	if got := SpotCap(box(0.14), 24, true); got == 0 {
		t.Fatal("span above threshold must be capped")
	}

	plain, h12, heavy := SpotCap(box(0.3), 0, false), SpotCap(box(0.3), 12, false), SpotCap(box(0.3), 24, true)
	if plain <= h12 || h12 <= heavy {
		t.Fatalf("cap must shrink with payload weight: %d %d %d", plain, h12, heavy)
	}
	if heavy*(featureBaseBytes+featureHourBytes*24+featureAreaBytes) > ResponseBudgetBytes {
		t.Fatalf("heavy cap %d exceeds budget", heavy)
	}

	if SpotCap(BBox{West: 0, South: 0, East: 0.05, North: 0.3}, 0, false) == 0 {
		t.Fatal("height must count towards the span")
	}
}

func TestListPageTruncation(t *testing.T) {
	repo := &listRepo{total: 1358, rows: 270}
	svc := NewService(repo)
	wide := BBox{West: 151, South: -34, East: 151.3, North: -33.7}
	res, err := svc.ListPage(context.Background(), ListQuery{BBox: &wide, Hours: 24, IncludeArea: true})
	if err != nil {
		t.Fatal(err)
	}
	if repo.limit == 0 || !res.Truncated || res.Total != 1358 || len(res.Items) != 270 {
		t.Fatalf("limit=%d res=%+v", repo.limit, res)
	}

	repo.total, repo.rows = 100, 100
	if res, _ = svc.ListPage(context.Background(), ListQuery{BBox: &wide}); res.Truncated {
		t.Fatal("100 spots under a cap must not report truncation")
	}

	narrow := BBox{West: 151, South: -34, East: 151.1, North: -33.9}
	_, _ = svc.ListPage(context.Background(), ListQuery{BBox: &narrow, Hours: 24})
	if repo.limit != 0 {
		t.Fatalf("narrow viewport limit=%d", repo.limit)
	}
	_, _ = svc.ListPage(context.Background(), ListQuery{IDs: []uuid.UUID{uuid.New()}})
	if repo.limit != 0 {
		t.Fatalf("ids limit=%d", repo.limit)
	}
	_, _ = svc.List(context.Background(), ListQuery{BBox: &wide})
	if repo.limit != 0 {
		t.Fatalf("List must not cap, limit=%d", repo.limit)
	}
}

func TestListSelectorAndLimits(t *testing.T) {
	svc := NewService(&listRepo{})
	bbox := BBox{West: 4.8, South: 52.3, East: 4.9, North: 52.4}
	id := uuid.New()
	ids51 := make([]uuid.UUID, 51)
	for i := range ids51 {
		ids51[i] = uuid.New()
	}
	for name, q := range map[string]ListQuery{
		"neither":  {},
		"both":     {BBox: &bbox, IDs: []uuid.UUID{id}},
		"51 ids":   {IDs: ids51},
		"hours 25": {BBox: &bbox, Hours: 25},
		"hours <0": {BBox: &bbox, Hours: -1},
	} {
		if _, err := svc.List(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%s: want ErrInvalidQuery, got %v", name, err)
		}
	}
	if _, err := svc.List(context.Background(), ListQuery{BBox: &BBox{West: 0, South: 0, East: 2, North: 2}}); !errors.Is(err, ErrBboxTooLarge) {
		t.Errorf("want ErrBboxTooLarge, got %v", err)
	}
}

func TestListHourWindow(t *testing.T) {
	repo := &listRepo{}
	svc := NewService(repo)
	bbox := BBox{West: 4.8, South: 52.3, East: 4.9, North: 52.4}
	// From must sit inside the score horizon (~now-24h .. now+8d).
	from := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Hour).Add(45 * time.Minute)
	if _, err := svc.List(context.Background(), ListQuery{BBox: &bbox, From: &from, Hours: 12}); err != nil {
		t.Fatal(err)
	}
	h9 := from.UTC().Truncate(time.Hour)
	if !repo.tsHour.Equal(h9) || !repo.from.Equal(h9) || !repo.to.Equal(h9.Add(12*time.Hour)) {
		t.Fatalf("window: ts=%v from=%v to=%v", repo.tsHour, repo.from, repo.to)
	}
	when := h9.Add(5 * time.Hour)
	_, _ = svc.List(context.Background(), ListQuery{BBox: &bbox, From: &from, When: &when, Hours: 1})
	if !repo.tsHour.Equal(when) {
		t.Fatalf("when must drive score hour, got %v", repo.tsHour)
	}
	_, _ = svc.List(context.Background(), ListQuery{BBox: &bbox})
	if !repo.from.IsZero() || !repo.to.IsZero() {
		t.Fatal("no series window without hours")
	}
}

func TestWeatherSourceCode(t *testing.T) {
	cases := map[string]string{"weatherkit": "weatherkit", "open-meteo": "openmeteo", "openmeteo": "openmeteo"}
	for in, want := range cases {
		if got := WeatherSourceCode(in); got == nil || *got != want {
			t.Errorf("%q -> %v, want %s", in, got, want)
		}
	}
	for _, in := range []string{"", "mystery"} {
		if WeatherSourceCode(in) != nil {
			t.Errorf("%q must map to nil", in)
		}
	}
}
