package digest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

type fakeRepo struct {
	cands    []Candidate
	spots    map[uuid.UUID]SpotInfo
	scores   map[uuid.UUID][]HourScore
	claimed  map[uuid.UUID]*time.Time
	released int
	resolve  map[uuid.UUID]uuid.UUID
	saved    []uuid.UUID
}

func (f *fakeRepo) Upsert(_ context.Context, id uuid.UUID, ids []uuid.UUID, en bool) (*Digest, error) {
	f.saved = ids
	return &Digest{DeviceID: id, SpotIDs: ids, Enabled: en, SendDow: 4, SendHour: 18}, nil
}
func (f *fakeRepo) Get(context.Context, uuid.UUID) (*Digest, error)       { return nil, ErrNotFound }
func (f *fakeRepo) Delete(context.Context, uuid.UUID) error               { return nil }
func (f *fakeRepo) DeviceExists(context.Context, uuid.UUID) (bool, error) { return true, nil }
func (f *fakeRepo) ResolveSpots(context.Context, []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	return f.resolve, nil
}
func (f *fakeRepo) ListCandidates(context.Context) ([]Candidate, error) { return f.cands, nil }
func (f *fakeRepo) Claim(_ context.Context, id uuid.UUID, d time.Time, prev *time.Time) (bool, error) {
	if cur, ok := f.claimed[id]; ok && cur != nil && cur.Equal(d) {
		return false, nil
	}
	f.claimed[id] = &d
	return true, nil
}
func (f *fakeRepo) Release(_ context.Context, id uuid.UUID, _ time.Time, prev *time.Time) error {
	f.released++
	f.claimed[id] = prev
	return nil
}
func (f *fakeRepo) Spots(_ context.Context, ids []uuid.UUID) ([]SpotInfo, error) {
	var out []SpotInfo
	for _, id := range ids {
		if s, ok := f.spots[id]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeRepo) Scores(_ context.Context, id uuid.UUID, from, to time.Time) ([]HourScore, error) {
	var out []HourScore
	for _, h := range f.scores[id] {
		if !h.Start.Before(from) && h.Start.Before(to) {
			out = append(out, h)
		}
	}
	return out, nil
}

type fakePlans map[uuid.UUID]string

func (p fakePlans) Plan(_ context.Context, id uuid.UUID) (string, error) { return p[id], nil }

type fakeSender struct {
	got []Delivery
	err error
}

func (s *fakeSender) Send(_ context.Context, d Delivery) error {
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, d)
	return nil
}

func TestRunWeekly(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	dev, devFree, devNoWin := uuid.New(), uuid.New(), uuid.New()
	spotA, spotB := uuid.New(), uuid.New()
	// Thursday 2026-11-19 18:10 Amsterdam.
	now := time.Date(2026, 11, 19, 18, 10, 0, 0, ams)

	sat := time.Date(2026, 11, 21, 0, 0, 0, 0, ams)
	sun := sat.AddDate(0, 0, 1)
	mk := func(day time.Time, startH int, scores ...int) []HourScore {
		return hs(day.Add(time.Duration(startH)*time.Hour), scores...)
	}
	scores := map[uuid.UUID][]HourScore{
		spotA: append(mk(sat, 14, 80, 82, 78), mk(sun, 10, 70, 71)...),
		spotB: mk(sun, 12, 90, 92, 91, 40),
	}
	newRepo := func() *fakeRepo {
		return &fakeRepo{
			cands: []Candidate{
				{Digest: Digest{DeviceID: dev, SpotIDs: []uuid.UUID{spotA, spotB}, Enabled: true, SendDow: 4, SendHour: 18}, DeviceTZ: "Europe/Amsterdam"},
				{Digest: Digest{DeviceID: devFree, SpotIDs: []uuid.UUID{spotA}, Enabled: true, SendDow: 4, SendHour: 18}, DeviceTZ: "Europe/Amsterdam"},
				{Digest: Digest{DeviceID: devNoWin, SpotIDs: []uuid.UUID{spotA}, Enabled: true, SendDow: 4, SendHour: 18}, DeviceTZ: "Europe/Amsterdam"},
			},
			spots: map[uuid.UUID]SpotInfo{
				spotA: {ID: spotA, Name: "Vondelpark", TZ: "Europe/Amsterdam"},
				spotB: {ID: spotB, Name: "Westerpark", TZ: "Europe/Amsterdam"},
			},
			scores:  scores,
			claimed: map[uuid.UUID]*time.Time{},
		}
	}
	plans := fakePlans{dev: "plus", devFree: "free", devNoWin: "plus"}

	t.Run("sends best window, skips free, silent when none", func(t *testing.T) {
		repo := newRepo()
		// devNoWin follows an empty spot.
		repo.cands[2].SpotIDs = []uuid.UUID{uuid.New()}
		snd := &fakeSender{}
		st, err := NewService(repo, plans, snd, nil).RunWeekly(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		if st.Sent != 1 || st.Skipped != 1 || st.NoWindow != 1 || st.Due != 3 {
			t.Fatalf("stats %+v", st)
		}
		d := snd.got[0]
		// Westerpark Sunday 13–15 local (92,91 → mean 91.5 → 92) beats Vondelpark Saturday (80).
		if d.SpotID != spotB || d.Score != 92 || d.WindowStart.Hour() != 12 || d.WindowEnd.Sub(d.WindowStart) != 2*time.Hour {
			t.Fatalf("delivery %+v", d)
		}
		if repo.claimed[devNoWin] == nil {
			t.Fatal("no-window device must still be marked as handled")
		}
	})

	t.Run("idempotent within the same local day", func(t *testing.T) {
		repo := newRepo()
		snd := &fakeSender{}
		svc := NewService(repo, plans, snd, nil)
		if _, err := svc.RunWeekly(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		first := len(snd.got)
		// Second run: candidates now carry last_sent_on like the DB would.
		for i := range repo.cands {
			repo.cands[i].LastSentOn = repo.claimed[repo.cands[i].DeviceID]
		}
		st, _ := svc.RunWeekly(context.Background(), now.Add(20*time.Minute))
		if len(snd.got) != first || st.Sent != 0 {
			t.Fatalf("resent: %d -> %d, stats %+v", first, len(snd.got), st)
		}
	})

	t.Run("claim race: loser does not send", func(t *testing.T) {
		repo := newRepo()
		d := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
		repo.claimed[dev] = &d // another replica won
		snd := &fakeSender{}
		if _, err := NewService(repo, plans, snd, nil).RunWeekly(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		for _, g := range snd.got {
			if g.DeviceID == dev {
				t.Fatal("lost claim but sent")
			}
		}
	})

	t.Run("transient send failure releases the slot", func(t *testing.T) {
		repo := newRepo()
		snd := &fakeSender{err: errors.New("boom")}
		st, _ := NewService(repo, plans, snd, nil).RunWeekly(context.Background(), now)
		if st.Failed == 0 || repo.released == 0 || repo.claimed[dev] != nil {
			t.Fatalf("stats %+v released=%d claimed=%v", st, repo.released, repo.claimed[dev])
		}
	})

	t.Run("dead token keeps the slot", func(t *testing.T) {
		repo := newRepo()
		snd := &fakeSender{err: alerts.ErrPushTokenDead}
		st, _ := NewService(repo, plans, snd, nil).RunWeekly(context.Background(), now)
		if st.Failed != 0 || repo.released != 0 || repo.claimed[dev] == nil {
			t.Fatalf("stats %+v released=%d", st, repo.released)
		}
	})

	t.Run("not send time", func(t *testing.T) {
		repo := newRepo()
		snd := &fakeSender{}
		st, _ := NewService(repo, plans, snd, nil).RunWeekly(context.Background(), now.Add(2*time.Hour))
		if st.Due != 0 || len(snd.got) != 0 {
			t.Fatalf("stats %+v", st)
		}
	})
}

func TestUpsertValidation(t *testing.T) {
	dev := uuid.New()
	a, b, merged := uuid.New(), uuid.New(), uuid.New()
	repo := &fakeRepo{resolve: map[uuid.UUID]uuid.UUID{a: a, b: b, merged: a}}
	svc := NewService(repo, fakePlans{dev: "plus"}, nil, nil)
	ctx := context.Background()

	d, err := svc.Upsert(ctx, dev, []uuid.UUID{merged, b, a}, true)
	if err != nil || len(d.SpotIDs) != 2 || d.SpotIDs[0] != a || d.SpotIDs[1] != b {
		t.Fatalf("merge resolve/dedup: %v %+v", err, d)
	}
	if _, err := svc.Upsert(ctx, dev, []uuid.UUID{uuid.New()}, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown id: %v", err)
	}
	many := make([]uuid.UUID, 21)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := svc.Upsert(ctx, dev, many, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf(">20: %v", err)
	}
	if _, err := svc.Upsert(ctx, dev, nil, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty+enabled: %v", err)
	}
	if _, err := svc.Upsert(ctx, dev, nil, false); err != nil {
		t.Fatalf("empty+disabled should be ok: %v", err)
	}
	free := NewService(repo, fakePlans{dev: "free"}, nil, nil)
	if _, err := free.Upsert(ctx, dev, []uuid.UUID{a}, true); !errors.Is(err, ErrPlanLimit) {
		t.Fatalf("free: %v", err)
	}
}
