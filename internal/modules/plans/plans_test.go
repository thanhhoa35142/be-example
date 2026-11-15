package plans

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)

func TestShareToken(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		tok, err := NewShareToken()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidToken(tok) {
			t.Fatalf("token %q does not match format", tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
	for _, bad := range []string{"", "short", "aaaaaaaaaaaaaaaaaaaaa+", "aaaaaaaaaaaaaaaaaaaaaaa", "../../../../etc/passwd"} {
		if ValidToken(bad) {
			t.Errorf("ValidToken(%q) = true", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	start := t0.Add(2 * time.Hour)
	cases := []struct {
		name string
		in   CreateInput
		code string
	}{
		{"ok", CreateInput{Start: start, End: start.Add(3 * time.Hour), TZ: "Asia/Ho_Chi_Minh"}, ""},
		{"spot tz default", CreateInput{Start: start, End: start.Add(3 * time.Hour)}, ""},
		{"bad tz", CreateInput{Start: start, End: start.Add(3 * time.Hour), TZ: "Mars/Olympus"}, CodeInvalidTZ},
		{"local tz", CreateInput{Start: start, End: start.Add(3 * time.Hour), TZ: "Local"}, CodeInvalidTZ},
		{"past", CreateInput{Start: t0.Add(-time.Minute), End: t0.Add(2 * time.Hour)}, CodeStartPast},
		{"now", CreateInput{Start: t0, End: t0.Add(2 * time.Hour)}, CodeStartPast},
		{"end before start", CreateInput{Start: start, End: start.Add(-time.Hour)}, CodeWindowInvalid},
		{"too short", CreateInput{Start: start, End: start.Add(30 * time.Minute)}, CodeWindowInvalid},
		{"too long", CreateInput{Start: start, End: start.Add(12*time.Hour + time.Minute)}, CodeWindowInvalid},
		{"12h ok", CreateInput{Start: start, End: start.Add(12 * time.Hour)}, ""},
		{"beyond horizon", CreateInput{Start: t0.Add(Horizon - time.Hour), End: t0.Add(Horizon + time.Hour)}, CodeBeyondHorizon},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := validate(c.in, "Asia/Ho_Chi_Minh", t0)
			if c.code == "" {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				if out.TZ == "" {
					t.Fatal("tz not defaulted")
				}
				return
			}
			var ve ValidationError
			if !errors.As(err, &ve) || ve.Code != c.code {
				t.Fatalf("got %v, want %s", err, c.code)
			}
		})
	}
	out, _, err := validate(CreateInput{Start: start.Add(17 * time.Second), End: start.Add(3*time.Hour + 59*time.Second)}, "UTC", t0)
	if err != nil || out.Start.Second() != 0 || out.End.Second() != 0 {
		t.Fatalf("times not truncated to minute: %v %v", out, err)
	}
}

func str(s string) *string { return &s }

func TestSummarizeClassify(t *testing.T) {
	cfg := DefaultConfig()
	if _, ok := Summarize(nil, cfg); ok {
		t.Fatal("empty window must not summarize")
	}
	w, _ := Summarize([]HourScore{{Score: 80}, {Score: 70}, {Score: 75}}, cfg)
	if w.Mean != 75 || w.Worst.Score != 70 || w.Cap != nil || w.Hours != 3 {
		t.Fatalf("summary %+v", w)
	}
	// Non-bad caps (e.g. "crowd") do not make the window bad.
	w2, _ := Summarize([]HourScore{{Score: 80, CapReason: str("crowd")}, {Score: 70, CapReason: str("rain")}}, cfg)
	if w2.Cap == nil || *w2.Cap != "rain" {
		t.Fatalf("cap %+v", w2.Cap)
	}
	base := 80
	cases := []struct {
		name string
		w    Window
		base *int
		want State
	}{
		{"ok", Window{Mean: 75}, &base, StateOK},
		{"rain cap", Window{Mean: 90, Cap: str("rain")}, &base, StateBad},
		{"below 60", Window{Mean: 59}, &base, StateBad},
		{"60 not bad", Window{Mean: 66}, &base, StateOK},
		{"drop 15 worse", Window{Mean: 65}, &base, StateWorse},
		{"drop 14 ok", Window{Mean: 66}, &base, StateOK},
		{"no baseline", Window{Mean: 61}, nil, StateOK},
	}
	for _, c := range cases {
		if got := Classify(c.w, c.base, cfg); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
	if !Acceptable(Window{Mean: 70}, Window{Mean: 50}, cfg) ||
		Acceptable(Window{Mean: 70, Cap: str("heat")}, Window{Mean: 50}, cfg) ||
		Acceptable(Window{Mean: 59}, Window{Mean: 50}, cfg) ||
		Acceptable(Window{Mean: 70}, Window{Mean: 70}, cfg) {
		t.Fatal("Acceptable rules")
	}
}

func TestStatus(t *testing.T) {
	p := Plan{Start: t0.Add(time.Hour), End: t0.Add(3 * time.Hour)}
	if s := p.Status(t0); s != "upcoming" {
		t.Fatal(s)
	}
	if s := p.Status(t0.Add(time.Hour)); s != "started" {
		t.Fatal(s)
	}
	if s := p.Status(t0.Add(3 * time.Hour)); s != "ended" {
		t.Fatal(s)
	}
	now := t0
	p.CanceledAt = &now
	if s := p.Status(t0); s != "canceled" {
		t.Fatal(s)
	}
}

type fakeRepo struct {
	spot     *SpotRef
	scores   []HourScore
	inserted *NewPlan
	limit    int
	shared   *Shared
}

func (f *fakeRepo) Spot(context.Context, uuid.UUID) (*SpotRef, error) {
	if f.spot == nil {
		return nil, ErrSpotNotFound
	}
	return f.spot, nil
}
func (f *fakeRepo) InsertLimited(_ context.Context, p NewPlan, max int) (uuid.UUID, error) {
	f.inserted, f.limit = &p, max
	return uuid.New(), nil
}
func (f *fakeRepo) ListForDevice(context.Context, uuid.UUID) ([]Plan, error) { return nil, nil }
func (f *fakeRepo) Cancel(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (f *fakeRepo) ActiveByToken(context.Context, string) (*Shared, error) {
	if f.shared == nil {
		return nil, ErrNotFound
	}
	c := *f.shared
	return &c, nil
}
func (f *fakeRepo) Follow(context.Context, string, uuid.UUID, int, int) (uuid.UUID, error) {
	return uuid.Nil, ErrNotFound
}
func (f *fakeRepo) Unfollow(context.Context, string, uuid.UUID) error { return nil }
func (f *fakeRepo) WindowScores(context.Context, uuid.UUID, time.Time, time.Time) ([]HourScore, error) {
	return f.scores, nil
}

type plusResolver struct{}

func (plusResolver) Plan(context.Context, uuid.UUID) (string, error) { return "plus", nil }

func TestCreateBaseline(t *testing.T) {
	repo := &fakeRepo{spot: &SpotRef{ID: uuid.New(), TZ: "Asia/Ho_Chi_Minh"},
		scores: []HourScore{{Score: 82}, {Score: 78}}}
	svc := NewService(repo, plusResolver{}, DefaultConfig())
	svc.now = func() time.Time { return t0 }
	_, _ = svc.Create(context.Background(), uuid.New(), CreateInput{Start: t0.Add(time.Hour), End: t0.Add(3 * time.Hour)})
	p := repo.inserted
	if p == nil || repo.limit != 20 {
		t.Fatalf("insert %+v limit %d", p, repo.limit)
	}
	if p.Baseline == nil || *p.Baseline != 80 || p.State == nil || *p.State != StateOK || !ValidToken(p.ShareToken) {
		t.Fatalf("baseline/state %+v", p)
	}

	// No scores yet: everything stays null until the first evaluation.
	repo.scores, repo.inserted = nil, nil
	svc.plans = nil
	_, _ = svc.Create(context.Background(), uuid.New(), CreateInput{Start: t0.Add(time.Hour), End: t0.Add(3 * time.Hour)})
	if repo.inserted.Baseline != nil || repo.inserted.State != nil || repo.limit != 1 {
		t.Fatalf("unscored %+v limit %d", repo.inserted, repo.limit)
	}

	// Already bad at creation (rain).
	repo.scores = []HourScore{{Score: 30, CapReason: str("rain")}}
	_, _ = svc.Create(context.Background(), uuid.New(), CreateInput{Start: t0.Add(time.Hour), End: t0.Add(3 * time.Hour)})
	if *repo.inserted.State != StateBad || *repo.inserted.CapReason != "rain" {
		t.Fatalf("bad at create %+v", repo.inserted)
	}
}

func TestSharedHidesBaselineAndRejectsBadTokens(t *testing.T) {
	base := 90
	sh := (&Shared{Start: t0, End: t0.Add(time.Hour)}).WithBaseline(&base)
	repo := &fakeRepo{shared: sh, scores: []HourScore{{Score: 70}}}
	svc := NewService(repo, nil, DefaultConfig())
	if _, err := svc.Shared(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad token: %v", err)
	}
	tok, _ := NewShareToken()
	got, err := svc.Shared(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.baseline != nil {
		t.Fatal("baseline leaked from service")
	}
	if got.State == nil || *got.State != StateWorse || got.Window.Mean != 70 {
		t.Fatalf("shared state %+v", got)
	}
}
