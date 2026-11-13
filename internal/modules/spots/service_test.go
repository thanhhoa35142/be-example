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
}

func (r *listRepo) List(_ context.Context, _ ListQuery, ts, from, to time.Time) ([]SpotWithScore, error) {
	r.tsHour, r.from, r.to = ts, from, to
	return nil, nil
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
	from := time.Date(2026, 11, 13, 10, 45, 0, 0, time.FixedZone("x", 3600)) // 09:45Z
	if _, err := svc.List(context.Background(), ListQuery{BBox: &bbox, From: &from, Hours: 12}); err != nil {
		t.Fatal(err)
	}
	h9 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
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
