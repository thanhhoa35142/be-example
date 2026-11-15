package plans

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ptrState(s State) *State { return &s }

func civilPtr(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestDecide(t *testing.T) {
	cfg := DefaultConfig()
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, ams) }
	// Plan: Saturday 21 Nov 2026 10:00–13:00 Amsterdam.
	start := at(2026, 11, 21, 10, 0)
	base := EvalPlan{Start: start, End: start.Add(3 * time.Hour), TZ: "Europe/Amsterdam", Notified: ptrState(StateOK)}
	with := func(f func(*EvalPlan)) EvalPlan { p := base; f(&p); return p }

	cases := []struct {
		name  string
		p     EvalPlan
		state State
		now   time.Time
		want  Action
	}{
		{"no change daytime", base, StateOK, at(2026, 11, 20, 12, 0), ActNone},
		{"change daytime", base, StateBad, at(2026, 11, 20, 12, 0), ActChange},
		{"change worse", base, StateWorse, at(2026, 11, 20, 12, 0), ActChange},
		{"back to ok is a change", with(func(p *EvalPlan) { p.Notified = ptrState(StateBad) }), StateOK, at(2026, 11, 20, 12, 0), ActChange},
		{"bad to worse is a change", with(func(p *EvalPlan) { p.Notified = ptrState(StateBad) }), StateWorse, at(2026, 11, 20, 12, 0), ActChange},
		// Quiet hours 22:00–07:00 local, wrapping midnight.
		{"quiet 22:00", base, StateBad, at(2026, 11, 20, 22, 0), ActDefer},
		{"quiet 23:59", base, StateBad, at(2026, 11, 20, 23, 59), ActDefer},
		{"quiet 00:30", base, StateBad, at(2026, 11, 21, 0, 30), ActDefer},
		{"quiet 06:59", base, StateBad, at(2026, 11, 21, 6, 59), ActDefer},
		{"quiet over 07:00", base, StateBad, at(2026, 11, 21, 7, 0), ActChange},
		{"21:59 still allowed", base, StateBad, at(2026, 11, 20, 21, 59), ActChange},
		// 72 h horizon edge.
		{"exactly 72h", base, StateBad, start.Add(-72 * time.Hour), ActChange},
		{"72h + 1min", base, StateBad, start.Add(-72*time.Hour - time.Minute), ActDefer},
		// One push per plan per local day.
		{"already pushed today", with(func(p *EvalPlan) { p.LastNotifiedOn = civilPtr(2026, 11, 20) }), StateBad, at(2026, 11, 20, 12, 0), ActDefer},
		{"pushed yesterday", with(func(p *EvalPlan) { p.LastNotifiedOn = civilPtr(2026, 11, 19) }), StateBad, at(2026, 11, 20, 12, 0), ActChange},
		// Reverted during deferral: state equals notified again → nothing.
		{"reverted after deferral", with(func(p *EvalPlan) { p.LastNotifiedOn = civilPtr(2026, 11, 20) }), StateOK, at(2026, 11, 20, 12, 0), ActNone},
		// Started / ended plans never notify.
		{"at start", base, StateBad, start, ActNone},
		{"started drops", base, StateBad, start.Add(time.Hour), ActNone},
		// Not yet baselined (no scores at creation): silent.
		{"no starting point", with(func(p *EvalPlan) { p.Notified = nil }), StateBad, at(2026, 11, 20, 12, 0), ActNone},
		// Confirm: day before, 18:00–22:00 local, ok/ok, once.
		{"confirm 18:00", base, StateOK, at(2026, 11, 20, 18, 0), ActConfirm},
		{"confirm 21:59", base, StateOK, at(2026, 11, 20, 21, 59), ActConfirm},
		{"no confirm 17:59", base, StateOK, at(2026, 11, 20, 17, 59), ActNone},
		{"no confirm 22:00", base, StateOK, at(2026, 11, 20, 22, 0), ActNone},
		{"no confirm two days before", base, StateOK, at(2026, 11, 19, 19, 0), ActNone},
		{"no confirm on the day", with(func(p *EvalPlan) { p.Start = at(2026, 11, 21, 20, 0); p.End = p.Start.Add(time.Hour) }), StateOK, at(2026, 11, 21, 18, 30), ActNone},
		{"no confirm twice", with(func(p *EvalPlan) { p.Confirmed = true }), StateOK, at(2026, 11, 20, 19, 0), ActNone},
		{"no confirm while worse", with(func(p *EvalPlan) { p.Notified = ptrState(StateWorse) }), StateWorse, at(2026, 11, 20, 19, 0), ActNone},
		// Change vs confirm on the same day: a change sent that day blocks
		// the confirm; a pending change wins over a confirm.
		{"change today blocks confirm", with(func(p *EvalPlan) { p.LastNotifiedOn = civilPtr(2026, 11, 20) }), StateOK, at(2026, 11, 20, 19, 0), ActNone},
		{"change wins over confirm", base, StateBad, at(2026, 11, 20, 19, 0), ActChange},
		{"confirm after ok on an earlier day", with(func(p *EvalPlan) {
			p.LastNotifiedOn = civilPtr(2026, 11, 19)
		}), StateOK, at(2026, 11, 20, 19, 0), ActConfirm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.p, c.state, c.now, ams, cfg); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

// Local date and quiet hours follow the plan's tz on a DST change day
// (Europe/Amsterdam falls back 25 Oct 2026 03:00 → 02:00).
func TestDecideDST(t *testing.T) {
	cfg := DefaultConfig()
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, ams) // Monday after fall-back
	p := EvalPlan{Start: start, End: start.Add(2 * time.Hour), Notified: ptrState(StateOK),
		LastNotifiedOn: civilPtr(2026, 10, 24)}
	// 25 Oct 06:30 local = 05:30 UTC (CET): still quiet.
	if got := Decide(p, StateBad, time.Date(2026, 10, 25, 5, 30, 0, 0, time.UTC), ams, cfg); got != ActDefer {
		t.Fatalf("06:30 local on DST day: %d", got)
	}
	// 25 Oct 07:00 local = 06:00 UTC: allowed, and the local date is the 25th.
	if got := Decide(p, StateBad, time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC), ams, cfg); got != ActChange {
		t.Fatalf("07:00 local on DST day: %d", got)
	}
	// Pushed on the 25th (local): another change at 23:30 UTC on the 25th
	// (00:30 local on the 26th) is quiet; at 07:00 local on the 26th it goes.
	p.LastNotifiedOn = civilPtr(2026, 10, 25)
	if got := Decide(p, StateWorse, time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC), ams, cfg); got != ActDefer {
		t.Fatalf("same local day: %d", got)
	}
	if got := Decide(p, StateWorse, time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC), ams, cfg); got != ActChange {
		t.Fatalf("next local day 07:00: %d", got)
	}
	// Confirm on the 25th at 18:00 local (17:00 UTC) for the 26th plan.
	p.LastNotifiedOn = nil
	if got := Decide(p, StateOK, time.Date(2026, 10, 25, 17, 0, 0, 0, time.UTC), ams, cfg); got != ActConfirm {
		t.Fatalf("confirm on DST day: %d", got)
	}
}

func hs(ts time.Time, scores ...int) []HourScore {
	out := make([]HourScore, len(scores))
	for i, s := range scores {
		out[i] = HourScore{TS: ts.Add(time.Duration(i) * time.Hour), Score: s}
	}
	return out
}

func TestBestTimeAlternative(t *testing.T) {
	cfg := DefaultConfig()
	day0 := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	dayEnd := day0.Add(24 * time.Hour)
	scores := make([]int, 24)
	for i := range scores {
		scores[i] = 50
	}
	scores[14], scores[15], scores[16] = 80, 85, 90 // best 3h window 14–17
	scores[8], scores[9], scores[10] = 75, 75, 75
	day := hs(day0, scores...)
	planStart := day0.Add(10 * time.Hour)
	cur, _ := Summarize(day[10:13], cfg)
	now := day0.Add(6 * time.Hour)

	a := BestTimeAlternative(day, planStart, 3*time.Hour, cur, now, dayEnd, cfg)
	if a == nil || a.Type != AltTime || !a.Start.Equal(day0.Add(14*time.Hour)) || !a.End.Equal(day0.Add(17*time.Hour)) || a.Score != 85 {
		t.Fatalf("got %+v", a)
	}
	// Past windows are not offered.
	if a := BestTimeAlternative(day, planStart, 3*time.Hour, cur, day0.Add(15*time.Hour), dayEnd, cfg); a != nil && a.Start.Before(day0.Add(15*time.Hour)) {
		t.Fatalf("past window offered: %+v", a)
	}
	// A capped hour disqualifies the window.
	rain := "rain"
	capped := append([]HourScore(nil), day...)
	capped[15].CapReason = &rain
	a = BestTimeAlternative(capped, planStart, 3*time.Hour, cur, now, dayEnd, cfg)
	if a == nil || !a.Start.Equal(day0.Add(16*time.Hour)) && !a.Start.Equal(day0.Add(8*time.Hour)) {
		t.Fatalf("capped: %+v", a)
	}
	// Must end by the end of the local day.
	if a := BestTimeAlternative(hs(day0.Add(21*time.Hour), 95, 95, 95), planStart, 3*time.Hour, cur, now, dayEnd, cfg); a == nil || !a.Start.Equal(day0.Add(21*time.Hour)) {
		t.Fatalf("21–24 window: %+v", a)
	}
	if a := BestTimeAlternative(hs(day0.Add(22*time.Hour), 95, 95), planStart, 3*time.Hour, cur, now, dayEnd, cfg); a != nil {
		t.Fatalf("crosses midnight: %+v", a)
	}
	// Gaps (missing hours) are not windows.
	gap := append(hs(day0.Add(14*time.Hour), 90), hs(day0.Add(16*time.Hour), 90, 90)...)
	if a := BestTimeAlternative(gap, planStart, 3*time.Hour, cur, now, dayEnd, cfg); a != nil {
		t.Fatalf("gap window: %+v", a)
	}
	// Nothing better than the current window → nil.
	good, _ := Summarize(hs(planStart, 95, 95, 95), cfg)
	if a := BestTimeAlternative(day, planStart, 3*time.Hour, good, now, dayEnd, cfg); a != nil {
		t.Fatalf("not better: %+v", a)
	}
}

// --- Evaluator with a fake repository ---

type evalFake struct {
	plans    []EvalPlan
	scores   map[uuid.UUID][]HourScore
	nearby   *Alternative
	saved    map[uuid.UUID]State
	savedAlt map[uuid.UUID]*Alternative
	claims   []Notification
	claimOK  bool
	queue    []Delivery
	marks    map[uuid.UUID]string
}

func (f *evalFake) EvaluablePlans(context.Context, time.Time) ([]EvalPlan, error) {
	return f.plans, nil
}
func (f *evalFake) WindowScores(_ context.Context, spot uuid.UUID, from, to time.Time) ([]HourScore, error) {
	var out []HourScore
	for _, h := range f.scores[spot] {
		if !h.TS.Before(from) && h.TS.Before(to) {
			out = append(out, h)
		}
	}
	return out, nil
}
func (f *evalFake) NearbyAlternative(context.Context, uuid.UUID, time.Time, time.Time, int, int, Config) (*Alternative, error) {
	return f.nearby, nil
}
func (f *evalFake) SaveEvaluation(_ context.Context, id uuid.UUID, s State, _ Window, alt *Alternative) error {
	f.saved[id] = s
	f.savedAlt[id] = alt
	return nil
}
func (f *evalFake) ClaimNotification(_ context.Context, n Notification, d time.Time) (bool, int64, error) {
	f.claims = append(f.claims, n)
	if !f.claimOK {
		return false, 0, nil
	}
	f.queue = append(f.queue, Delivery{PlanID: n.PlanID, DeviceID: uuid.New(), LocalDate: d, Notification: n},
		Delivery{PlanID: n.PlanID, DeviceID: uuid.New(), LocalDate: d, Notification: n})
	return true, 2, nil
}
func (f *evalFake) PrepareDeliveries(context.Context, int) error { return nil }
func (f *evalFake) ClaimDeliveries(context.Context, int, int) ([]Delivery, error) {
	q := f.queue
	f.queue = nil
	return q, nil
}
func (f *evalFake) MarkDeliverySent(_ context.Context, d Delivery) error {
	f.marks[d.DeviceID] = "sent"
	return nil
}
func (f *evalFake) MarkDeliveryFailed(_ context.Context, d Delivery, status string, _ *int, _ string) error {
	f.marks[d.DeviceID] = status
	return nil
}

type senderFake struct {
	err  error
	sent []Notification
}

func (s *senderFake) SendPlan(_ context.Context, _ uuid.UUID, n Notification) error {
	s.sent = append(s.sent, n)
	return s.err
}

func newEvalFake() *evalFake {
	return &evalFake{scores: map[uuid.UUID][]HourScore{}, saved: map[uuid.UUID]State{},
		savedAlt: map[uuid.UUID]*Alternative{}, marks: map[uuid.UUID]string{}, claimOK: true}
}

func TestEvaluatorChangeWithTimeAlternative(t *testing.T) {
	now := time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC) // 12:00 Amsterdam
	spot := uuid.New()
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC) // 10:00 local
	day := make([]int, 24)
	for i := range day {
		day[i] = 50
	}
	day[13], day[14], day[15] = 85, 85, 85 // 13:00–16:00 local (12:00 UTC)
	f := newEvalFake()
	// Local day 21 Nov Amsterdam = 20 Nov 23:00 UTC .. 21 Nov 23:00 UTC.
	f.scores[spot] = hs(time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC), day...)
	base := 80
	p := EvalPlan{ID: uuid.New(), SpotID: spot, SpotName: "Vondelpark", SpotKind: "park", Start: start, End: start.Add(3 * time.Hour),
		TZ: "Europe/Amsterdam", Baseline: &base, Notified: ptrState(StateOK)}
	f.plans = []EvalPlan{p}
	snd := &senderFake{}
	e := NewEvaluator(f, snd, DefaultConfig(), nil)
	e.now = func() time.Time { return now }
	st, err := e.Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.saved[p.ID] != StateBad || len(f.claims) != 1 || f.claims[0].Kind != KindChanged {
		t.Fatalf("saved %v claims %+v", f.saved, f.claims)
	}
	alt := f.claims[0].Alternative
	if alt == nil || alt.Type != AltTime || !alt.Start.Equal(time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)) || alt.Score != 85 {
		t.Fatalf("alternative %+v", alt)
	}
	if st.Changed != 1 || st.Queued != 2 || st.Sent != 2 || len(snd.sent) != 2 {
		t.Fatalf("stats %+v sent %d", st, len(snd.sent))
	}
}

func TestEvaluatorNearbyThenNone(t *testing.T) {
	now := time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)
	spot := uuid.New()
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	f := newEvalFake()
	f.scores[spot] = hs(start, 40, 40, 40) // only the plan window, no better time
	other := uuid.New()
	f.nearby = &Alternative{Type: AltSpot, SpotID: &other, SpotName: "Oosterpark", Score: 78}
	p := EvalPlan{ID: uuid.New(), SpotID: spot, Start: start, End: start.Add(3 * time.Hour), TZ: "Europe/Amsterdam", Notified: ptrState(StateOK)}
	f.plans = []EvalPlan{p}
	e := NewEvaluator(f, &senderFake{}, DefaultConfig(), nil)
	e.now = func() time.Time { return now }
	if _, err := e.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a := f.savedAlt[p.ID]; a == nil || a.Type != AltSpot || a.SpotName != "Oosterpark" {
		t.Fatalf("nearby: %+v", a)
	}
	f.nearby = nil
	f.claims = nil
	if _, err := e.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a := f.savedAlt[p.ID]; a == nil || a.Type != AltNone {
		t.Fatalf("none: %+v", a)
	}
}

func TestEvaluatorSilentAndDeferred(t *testing.T) {
	spot := uuid.New()
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	f := newEvalFake()
	f.scores[spot] = hs(start, 30, 30, 30)
	silent := EvalPlan{ID: uuid.New(), SpotID: spot, Start: start, End: start.Add(3 * time.Hour), TZ: "UTC"}
	night := EvalPlan{ID: uuid.New(), SpotID: spot, Start: start, End: start.Add(3 * time.Hour), TZ: "UTC", Notified: ptrState(StateOK)}
	noScores := EvalPlan{ID: uuid.New(), SpotID: uuid.New(), Start: start, End: start.Add(time.Hour), TZ: "UTC", Notified: ptrState(StateOK)}
	f.plans = []EvalPlan{silent, night, noScores}
	snd := &senderFake{}
	e := NewEvaluator(f, snd, DefaultConfig(), nil)
	e.now = func() time.Time { return time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC) }
	st, _ := e.Evaluate(context.Background())
	if len(f.claims) != 0 || len(snd.sent) != 0 {
		t.Fatalf("sent at night / for a silent plan: %+v", f.claims)
	}
	if st.Deferred != 1 || st.NoScores != 1 || f.saved[silent.ID] != StateBad {
		t.Fatalf("stats %+v saved %v", st, f.saved)
	}
	if _, ok := f.saved[noScores.ID]; ok {
		t.Fatal("plan without scores was saved")
	}
}

func TestEvaluatorLostClaimAndSendErrors(t *testing.T) {
	spot := uuid.New()
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	newRun := func(claimOK bool, err error) (*evalFake, EvalStats) {
		f := newEvalFake()
		f.claimOK = claimOK
		f.scores[spot] = hs(start, 30, 30, 30)
		f.plans = []EvalPlan{{ID: uuid.New(), SpotID: spot, Start: start, End: start.Add(3 * time.Hour), TZ: "UTC", Notified: ptrState(StateOK)}}
		e := NewEvaluator(f, &senderFake{err: err}, DefaultConfig(), nil)
		e.now = func() time.Time { return now }
		st, _ := e.Evaluate(context.Background())
		return f, st
	}
	if _, st := newRun(false, nil); st.Changed != 0 || st.Sent != 0 {
		t.Fatalf("lost claim still counted: %+v", st)
	}
	f, st := newRun(true, ErrPushTokenDead)
	for _, m := range f.marks {
		if m != "dead" {
			t.Fatalf("410 mark %v", f.marks)
		}
	}
	if st.Dead != 2 {
		t.Fatalf("stats %+v", st)
	}
	f, st = newRun(true, errors.New("apns 500"))
	for _, m := range f.marks {
		if m != "failed" {
			t.Fatalf("500 mark %v", f.marks)
		}
	}
	if st.Failed != 2 {
		t.Fatalf("stats %+v", st)
	}
	if _, st = newRun(true, ErrNoPushToken); st.Dead != 2 {
		t.Fatalf("no token %+v", st)
	}
}
