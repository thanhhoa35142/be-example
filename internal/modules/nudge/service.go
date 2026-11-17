package nudge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

type Config struct {
	MinScore int // mean score a good day needs (NUDGE_MIN_SCORE)
	RadiusM  int // search radius around the home cell center (NUDGE_RADIUS_M)
}

// postSendTimeout bounds the status writes that follow a send. They run on a
// context detached from the worker's, so a shutdown that lands between the
// APNs call and the write cannot lose the record of a push that already left.
const postSendTimeout = 5 * time.Second

type Service struct {
	repo   Repository
	sender Sender
	plans  digest.PlanResolver
	log    *slog.Logger
	cfg    Config
}

// plans decides whether a device with the weekly digest on really gets the
// digest (Plus only, like the digest job); nil means "assume it does".
func NewService(repo Repository, sender Sender, plans digest.PlanResolver, log *slog.Logger, cfg Config) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, sender: sender, plans: plans, log: log, cfg: cfg}
}

// ---- subscription use cases ----

func validCoord(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsInf(lat, 0) && !math.IsNaN(lng) && !math.IsInf(lng, 0) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

// Put creates or updates the subscription. The coordinate is converted to an
// H3 cell here and never leaves this function.
func (s *Service) Put(ctx context.Context, deviceID uuid.UUID, in PutInput) (*Subscription, error) {
	if in.SendHour != nil && (*in.SendHour < MinSendHour || *in.SendHour > MaxSendHour) {
		return nil, fmt.Errorf("%w: sendHour must be %d-%d", ErrInvalid, MinSendHour, MaxSendHour)
	}
	if in.Consent != nil && *in.Consent != ConsentExplicit && *in.Consent != ConsentProvisional {
		return nil, fmt.Errorf("%w: consent must be %q or %q", ErrInvalid, ConsentExplicit, ConsentProvisional)
	}
	var cell *int64
	if in.Home != nil {
		if in.Home.Source != SourceLocation && in.Home.Source != SourceCity {
			return nil, fmt.Errorf("%w: home.source must be %q or %q", ErrInvalid, SourceLocation, SourceCity)
		}
		if !validCoord(in.Home.Lat, in.Home.Lng) {
			return nil, fmt.Errorf("%w: home coordinates out of range", ErrInvalid)
		}
		c, err := geo.CellIDFromLatLng(in.Home.Lat, in.Home.Lng)
		if err != nil {
			return nil, fmt.Errorf("%w: home coordinates out of range", ErrInvalid)
		}
		cell = &c
	}

	ok, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrDeviceInvalid
	}

	up := SubscriptionUpsert{DeviceID: deviceID, Enabled: true, SendHour: DefaultSendHour, HomeCell: cell}
	cur, err := s.repo.Get(ctx, deviceID)
	switch {
	case errors.Is(err, ErrNotFound):
		if in.Consent == nil {
			return nil, fmt.Errorf("%w: consent is required to opt in", ErrInvalid)
		}
		up.Consent = *in.Consent
		if in.Home != nil {
			up.HomeSource = in.Home.Source
		}
	case err != nil:
		return nil, err
	default:
		up.Enabled, up.SendHour, up.Consent = cur.Enabled, cur.SendHour, cur.Consent
		if in.Consent != nil {
			up.Consent = *in.Consent
		}
		if in.Home != nil {
			up.HomeSource = in.Home.Source
		}
	}
	if in.Enabled != nil {
		up.Enabled = *in.Enabled
	}
	if in.SendHour != nil {
		up.SendHour = *in.SendHour
	}
	return s.repo.Upsert(ctx, up)
}

func (s *Service) Get(ctx context.Context, deviceID uuid.UUID) (*Subscription, error) {
	return s.repo.Get(ctx, deviceID)
}

func (s *Service) Delete(ctx context.Context, deviceID uuid.UUID) error {
	return s.repo.Delete(ctx, deviceID)
}

// ---- periodic run ----

type due struct {
	c         Candidate
	slot      string
	localDate time.Time // device-local day: idempotency key and 20 h cap, not the scored day
	retry     bool
	// planCheck marks a Friday weekend slot of a device with the weekly digest
	// on. The digest replaces the nudge only when it would really be sent
	// (Plus), which needs a plan lookup, so Run resolves it.
	planCheck bool
}

// dayPick is the best window of one local day around a cell.
type dayPick struct {
	spotID   uuid.UUID
	name, tz string
	quality  int
	dist     float64
	win      digest.Window
}

// weekVec holds the best pick of home..home+HorizonDays-1 (nil = no window).
// home is the calendar date "today" in the home area's time zone (the zone of
// the top-ranked spot near the cell), which can differ from the device's date
// when the user is traveling. The zero value means no spot to judge by.
type weekVec struct {
	home time.Time
	days [HorizonDays]*dayPick
	err  error
}

// civil returns the calendar date of t in its own location as UTC midnight.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func sameISOWeek(a, b time.Time) bool {
	ay, aw := a.ISOWeek()
	by, bw := b.ISOWeek()
	return ay == by && aw == bw
}

// classify decides whether a candidate is due now and in which slot. The
// device tz is client text, so it is resolved here and a bad zone means "not
// due". skip reports a device that was due but excluded by a digest rule.
func classify(c Candidate, now time.Time) (d due, isDue, skip bool) {
	loc, err := time.LoadLocation(c.TZ)
	if err != nil || c.TZ == "" {
		return due{}, false, false
	}
	local := now.In(loc)
	date := civil(local)

	slot := ""
	switch {
	case local.Weekday() == time.Friday && local.Hour() == WeekendSendHour:
		slot = SlotWeekend
	case local.Hour() == c.SendHour:
		slot = SlotDay
	default:
		return due{}, false, false
	}

	retry := false
	if c.LastDeliveryDate != nil && c.LastDeliveryDate.Equal(date) {
		// Same local day already has a delivery: only a failed one with
		// attempts left may be retried.
		if c.LastDeliveryStatus != "failed" || c.LastDeliveryAttempts >= MaxDeliveryAttempts {
			return due{}, false, false
		}
		retry = true
	}

	switch slot {
	case SlotWeekend:
		// A device with the weekly digest on gets the digest instead.
		if c.DigestEnabled {
			return due{c: c, slot: slot, localDate: date, retry: retry, planCheck: true}, true, false
		}
	case SlotDay:
		if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
			if c.WeekendNudgeDate != nil && sameISOWeek(*c.WeekendNudgeDate, date) {
				return due{}, true, true
			}
			if c.DigestSentAt != nil &&
				sameISOWeek(civil(c.DigestSentAt.In(loc)), date) {
				return due{}, true, true
			}
		}
	}
	return due{c: c, slot: slot, localDate: date, retry: retry}, true, false
}

// Run sends the nudges that are due at `now`. Safe to run every 15 minutes:
// one delivery per device and local day is enforced by the claim.
func (s *Service) Run(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	now = now.UTC()
	if err := s.repo.Sweep(ctx, now); err != nil {
		s.log.Error("nudge sweep failed", "error", err)
	}
	cands, err := s.repo.Candidates(ctx, now)
	if err != nil {
		return st, err
	}
	st.Candidates = len(cands)

	var dues []due
	for _, c := range cands {
		d, isDue, skip := classify(c, now)
		switch {
		case !isDue:
		case skip:
			st.Due++
			st.Skipped++
		default:
			st.Due++
			dues = append(dues, d)
		}
	}
	if len(dues) == 0 {
		return st, nil
	}

	// Scores are loaded once per home cell: the scored day is the home area's
	// own "today", so it does not depend on the device's time zone.
	memo := map[int64]*weekVec{}
	for _, d := range dues {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		log := s.log.With("device", d.c.DeviceID)
		if d.planCheck {
			sends, err := s.digestWillSend(ctx, d.c.DeviceID)
			if err != nil {
				log.Error("nudge plan lookup failed", "error", err)
				st.Failed++
				continue
			}
			if sends {
				st.Skipped++
				continue
			}
		}
		vec, ok := memo[d.c.HomeCell]
		if !ok {
			vec = s.weekVector(ctx, d.c.HomeCell, now)
			memo[d.c.HomeCell] = vec
			st.Groups++
		}
		if vec.err != nil {
			log.Error("nudge scores failed", "error", vec.err)
			st.Failed++
			continue
		}
		del, found := s.choose(vec, d)
		if !found {
			st.NoWindow++
			continue
		}
		s.deliver(ctx, log, d, del, now, &st)
	}
	return st, nil
}

// digestWillSend reports whether the weekly digest replaces the Friday nudge:
// the same entitlement the digest job applies (only Plus gets a digest). A
// device that turned the digest on and later lost Plus gets the nudge.
func (s *Service) digestWillSend(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	if s.plans == nil {
		return true, nil
	}
	plan, err := s.plans.Plan(ctx, deviceID)
	if err != nil {
		return false, err
	}
	return plan == "plus", nil
}

// choose picks the day (and spot) for a device and applies the "unusual day"
// rule: mean >= MinScore and (top TopDays of the horizon or mean >= 90).
func (s *Service) choose(vec *weekVec, d due) (Delivery, bool) {
	var targets []int
	switch d.slot {
	case SlotDay:
		targets = []int{0}
	case SlotWeekend:
		// Sent on the device's Friday evening, when the home date can still be
		// Thursday, Friday or Saturday: take the Saturday and Sunday of the
		// home area wherever they fall in the next days.
		for i := 0; i < 4 && i < HorizonDays; i++ {
			if wd := vec.home.AddDate(0, 0, i).Weekday(); wd == time.Saturday || wd == time.Sunday {
				targets = append(targets, i)
			}
		}
	}
	best := -1
	for _, i := range targets {
		p := vec.days[i]
		if p == nil || p.win.Mean < float64(s.cfg.MinScore) {
			continue
		}
		greater := 0
		for _, o := range vec.days {
			if o != nil && o.win.Mean > p.win.Mean {
				greater++
			}
		}
		if greater >= TopDays && p.win.Mean < ExceptionalMean {
			continue
		}
		if best < 0 || p.win.Mean > vec.days[best].win.Mean {
			best = i
		}
	}
	if best < 0 {
		return Delivery{}, false
	}
	p := vec.days[best]
	return Delivery{
		DeviceID:    d.c.DeviceID,
		Slot:        d.slot,
		LocalDate:   d.localDate,
		TargetDate:  vec.home.AddDate(0, 0, best),
		SpotID:      p.spotID,
		SpotName:    p.name,
		SpotTZ:      p.tz,
		WindowStart: p.win.Start.UTC(),
		WindowEnd:   p.win.End.UTC(),
		Score:       int(math.Round(p.win.Mean)),
	}, true
}

func (s *Service) deliver(ctx context.Context, log *slog.Logger, d due, del Delivery, now time.Time, st *Stats) {
	var (
		claimed bool
		err     error
	)
	if d.retry {
		claimed, err = s.repo.Reclaim(ctx, del, now)
	} else {
		claimed, err = s.repo.Claim(ctx, del, now)
	}
	if err != nil {
		log.Error("nudge claim failed", "error", err)
		st.Failed++
		return
	}
	if !claimed {
		st.Skipped++
		return
	}

	err = s.sender.Send(ctx, del)
	// The send is done (or failed): record it even when the worker is being
	// stopped, otherwise the push has left without a ledger row and goes out
	// again after restart.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postSendTimeout)
	defer cancel()
	switch {
	case err == nil:
		// The ledger row goes in BEFORE MarkSent: if MarkSent fails the send
		// has already left, and the 20 h any-push cap then blocks a retry.
		if rerr := s.repo.RecordPushSend(wctx, del.DeviceID, Kind, now); rerr != nil {
			log.Error("nudge push ledger failed", "error", rerr)
		}
		if merr := s.repo.MarkSent(wctx, del, now, d.c.IgnoredStreak); merr != nil {
			log.Error("nudge mark sent failed", "error", merr)
		}
		st.Sent++
	case errors.Is(err, alerts.ErrNoPushToken), errors.Is(err, alerts.ErrPushTokenDead):
		if merr := s.repo.MarkDead(wctx, del, err.Error()); merr != nil {
			log.Error("nudge mark dead failed", "error", merr)
		}
		st.Skipped++
	default:
		log.Error("nudge send failed", "error", err)
		if merr := s.repo.MarkFailed(wctx, del, err.Error()); merr != nil {
			log.Error("nudge mark failed failed", "error", merr)
		}
		st.Failed++
	}
}

// weekVector loads the scores around a cell once and finds the best spot and
// window for each of the next HorizonDays local days.
func (s *Service) weekVector(ctx context.Context, cell int64, now time.Time) *weekVec {
	vec := &weekVec{}
	// The home date is not known before the rows are (it depends on the zone of
	// the top spot), so read from now to a horizon that covers the last local
	// day in any zone: home date <= UTC date + 1, its day HorizonDays-1 ends by
	// +HorizonDays+12 h. Hours before now are never used.
	from := now.Truncate(time.Hour)
	to := civil(now).AddDate(0, 0, HorizonDays+3)
	rows, err := s.repo.ScoresNear(ctx, cell, s.cfg.RadiusM, from, to)
	if err != nil {
		vec.err = err
		return vec
	}

	type spotDay struct {
		spot uuid.UUID
		day  int
	}
	byKey := map[spotDay][]digest.HourScore{}
	meta := map[uuid.UUID]SpotHour{}
	locs := map[string]*time.Location{}
	locOf := func(tz string) *time.Location {
		loc, ok := locs[tz]
		if !ok {
			var lerr error
			if loc, lerr = time.LoadLocation(tz); lerr != nil {
				loc = nil
			}
			locs[tz] = loc
		}
		return loc
	}

	// Home zone: the zone of the top-ranked spot (quality, distance, id: the
	// same order the query uses to pick its 50) that has a usable zone.
	var top *SpotHour
	for i := range rows {
		r := &rows[i]
		if locOf(r.TZ) == nil {
			continue
		}
		if top == nil || rankedBefore(r, top) {
			top = r
		}
	}
	if top == nil {
		return vec
	}
	vec.home = civil(now.In(locOf(top.TZ)))

	for _, r := range rows {
		loc := locOf(r.TZ)
		if loc == nil || r.Start.Before(now) {
			continue
		}
		lh := r.Start.In(loc)
		if lh.Hour() < DayStartHour || lh.Hour() >= DayEndHour {
			continue
		}
		idx := int(civil(lh).Sub(vec.home).Hours() / 24)
		if idx < 0 || idx >= HorizonDays {
			continue
		}
		k := spotDay{r.SpotID, idx}
		byKey[k] = append(byKey[k], digest.HourScore{Start: r.Start, Score: r.Score})
		meta[r.SpotID] = r
	}

	// Deterministic iteration: sort keys.
	keys := make([]spotDay, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].day != keys[j].day {
			return keys[i].day < keys[j].day
		}
		return keys[i].spot.String() < keys[j].spot.String()
	})
	for _, k := range keys {
		w, ok := digest.BestWindow(byKey[k], MinHourScore, MinWindow, MaxWindow)
		if !ok {
			continue
		}
		m := meta[k.spot]
		cand := &dayPick{spotID: m.SpotID, name: m.Name, tz: m.TZ, quality: m.Quality, dist: m.DistanceM, win: w}
		if cur := vec.days[k.day]; cur == nil || better(cand, cur) {
			vec.days[k.day] = cand
		}
	}
	return vec
}

// rankedBefore orders spots the way the query ranks them: higher quality,
// then nearer, then lower id.
func rankedBefore(a, b *SpotHour) bool {
	switch {
	case a.Quality != b.Quality:
		return a.Quality > b.Quality
	case a.DistanceM != b.DistanceM:
		return a.DistanceM < b.DistanceM
	default:
		return a.SpotID.String() < b.SpotID.String()
	}
}

// better ranks two picks of the same day: higher mean, then higher quality,
// then nearer, then lower id (stable).
func better(a, b *dayPick) bool {
	switch {
	case a.win.Mean != b.win.Mean:
		return a.win.Mean > b.win.Mean
	case a.quality != b.quality:
		return a.quality > b.quality
	case a.dist != b.dist:
		return a.dist < b.dist
	default:
		return a.spotID.String() < b.spotID.String()
	}
}
