package plans

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
)

// Push kinds of plans.evaluate (docs/PUSH_KEYS.md).
const (
	KindChanged   = "plan.changed"
	KindConfirmed = "plan.confirmed"
)

// MaxDeliveryAttempts per (plan, device, local day) push.
const MaxDeliveryAttempts = 3

// deliveryBatch caps the pushes sent per run; the rest wait for the next.
const deliveryBatch = 1000

var (
	// ErrNoPushToken: the device has no active push token (dead, not retried).
	ErrNoPushToken = errors.New("plans: no push token")
	// ErrPushTokenDead: APNs 410; the sender has unregistered the device.
	ErrPushTokenDead = errors.New("plans: push token dead")
)

// EvalPlan is a plan as plans.evaluate sees it: not canceled, not started.
type EvalPlan struct {
	ID         uuid.UUID
	ShareToken string
	SpotID     uuid.UUID
	SpotName   string
	SpotKind   string
	Start, End time.Time
	TZ         string
	Baseline   *int
	// Notified is the last state pushed (or set silently as the starting
	// point); nil until the plan was first evaluated with scores.
	Notified *State
	// LastNotifiedOn is the plan-local date of the last push, as a civil
	// date (UTC midnight).
	LastNotifiedOn *time.Time
	Confirmed      bool
}

// Notification is the language-neutral content of one plan push, stored in
// the outbox; the notifier turns it into loc keys at send time.
type Notification struct {
	Kind        string       `json:"kind"`
	PlanID      uuid.UUID    `json:"planId"`
	ShareToken  string       `json:"shareToken"`
	SpotID      uuid.UUID    `json:"spotId"`
	SpotName    string       `json:"spotName,omitempty"`
	SpotKind    string       `json:"spotKind"`
	Start       time.Time    `json:"start"`
	End         time.Time    `json:"end"`
	TZ          string       `json:"tz"`
	State       State        `json:"state"`
	Score       int          `json:"score"`
	CapReason   *string      `json:"capReason,omitempty"`
	Alternative *Alternative `json:"alternative,omitempty"`
}

// Delivery is one claimed outbox row.
type Delivery struct {
	PlanID    uuid.UUID
	DeviceID  uuid.UUID
	LocalDate time.Time
	Notification
}

type EvalRepository interface {
	EvaluablePlans(ctx context.Context, until time.Time) ([]EvalPlan, error)
	WindowScores(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]HourScore, error)
	// NearbyAlternative: best other spot within cfg.AltRadiusM for [from, to)
	// with all hours scored, no bad cap and mean >= minMean; nil if none.
	NearbyAlternative(ctx context.Context, spotID uuid.UUID, from, to time.Time, hours, minMean int, cfg Config) (*Alternative, error)
	SaveEvaluation(ctx context.Context, id uuid.UUID, state State, w Window, alt *Alternative) error
	// ClaimNotification atomically moves the plan's notified state (change)
	// or confirmed flag (confirm) and queues one delivery per recipient
	// with active push. claimed is false when another run got there first.
	ClaimNotification(ctx context.Context, n Notification, localDate time.Time) (claimed bool, queued int64, err error)
	// PrepareDeliveries: stale claims back to failed, undeliverable rows
	// (plan canceled/started, local day over, attempts exhausted) to dead.
	PrepareDeliveries(ctx context.Context, maxAttempts int) error
	ClaimDeliveries(ctx context.Context, maxAttempts, batch int) ([]Delivery, error)
	MarkDeliverySent(ctx context.Context, d Delivery) error
	// MarkDeliveryFailed sets status "failed" (retried) or "dead".
	MarkDeliveryFailed(ctx context.Context, d Delivery, status string, apnsStatus *int, msg string) error
}

type Sender interface {
	SendPlan(ctx context.Context, deviceID uuid.UUID, n Notification) error
}

type EvalStats struct {
	Plans, NoScores, Changed, Confirmed, Deferred, Queued, Sent, Failed, Dead int
}

type Evaluator struct {
	repo   EvalRepository
	sender Sender
	cfg    Config
	now    func() time.Time
	logger *slog.Logger
}

func NewEvaluator(repo EvalRepository, sender Sender, cfg Config, logger *slog.Logger) *Evaluator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Evaluator{repo: repo, sender: sender, cfg: cfg, now: time.Now, logger: logger}
}

// Action is what Decide asks for one plan in one run.
type Action int

const (
	ActNone Action = iota
	// ActDefer: a change is pending but may not be sent now (beyond the
	// notify horizon, quiet hours, or already pushed this local day).
	ActDefer
	ActChange
	ActConfirm
)

// localDate is the calendar date of t in loc, as UTC midnight.
func localDate(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func inQuiet(hour int, cfg Config) bool {
	if cfg.QuietStart > cfg.QuietEnd {
		return hour >= cfg.QuietStart || hour < cfg.QuietEnd
	}
	return hour >= cfg.QuietStart && hour < cfg.QuietEnd
}

// Decide is the push policy, pure:
//   - nothing once the plan has started, or before its starting point
//     (Notified) is set;
//   - a change (state != Notified) is sent only within NotifyHorizon of the
//     start, outside quiet hours, and if nothing was pushed for the plan
//     this local day; otherwise it is deferred and re-decided next run (if
//     the state reverts meanwhile, nothing is sent);
//   - plan.confirmed: once, on the local day before the start, from
//     ConfirmFrom until QuietStart, while state and Notified are both ok and
//     nothing was pushed that day.
//
// "Local" is the plan's timezone throughout.
func Decide(p EvalPlan, state State, now time.Time, loc *time.Location, cfg Config) Action {
	if !now.Before(p.Start) || p.Notified == nil {
		return ActNone
	}
	local := now.In(loc)
	today := localDate(now, loc)
	sentToday := p.LastNotifiedOn != nil && p.LastNotifiedOn.Equal(today)
	if state != *p.Notified {
		if p.Start.Sub(now) > cfg.NotifyHorizon || inQuiet(local.Hour(), cfg) || sentToday {
			return ActDefer
		}
		return ActChange
	}
	if state == StateOK && !p.Confirmed && !sentToday &&
		localDate(p.Start, loc).Sub(today) == 24*time.Hour &&
		local.Hour() >= cfg.ConfirmFrom && !inQuiet(local.Hour(), cfg) {
		return ActConfirm
	}
	return ActNone
}

// BestTimeAlternative: the best window of the plan's length at the same
// spot, starting on the hour later than now and ending by dayEnd (the end
// of the plan's local day), every hour scored and Acceptable against the
// current window. Earliest wins ties. day must be sorted by TS.
func BestTimeAlternative(day []HourScore, planStart time.Time, dur time.Duration, cur Window, now, dayEnd time.Time, cfg Config) *Alternative {
	need := int(math.Ceil(dur.Hours()))
	planHour := planStart.UTC().Truncate(time.Hour)
	var best *Alternative
	bestMean := -1
	for i := range day {
		start := day[i].TS
		end := start.Add(dur)
		if !start.After(now) || start.Equal(planHour) || end.After(dayEnd) {
			continue
		}
		if i+need > len(day) || !day[i+need-1].TS.Equal(start.Add(time.Duration(need-1)*time.Hour)) {
			continue
		}
		w, _ := Summarize(day[i:i+need], cfg)
		if !Acceptable(w, cur, cfg) || w.Mean <= bestMean {
			continue
		}
		s, e := start, end
		best = &Alternative{Type: AltTime, Start: &s, End: &e, Score: w.Mean}
		bestMean = w.Mean
	}
	return best
}

func (e *Evaluator) alternative(ctx context.Context, p EvalPlan, cur Window, now time.Time, loc *time.Location) *Alternative {
	dur := p.End.Sub(p.Start)
	ls := p.Start.In(loc)
	dayStart := time.Date(ls.Year(), ls.Month(), ls.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	day, err := e.repo.WindowScores(ctx, p.SpotID, dayStart.UTC(), dayEnd.UTC())
	if err != nil {
		e.logger.WarnContext(ctx, "plan alternative: day scores failed", "plan", p.ID, "error", err)
	} else if a := BestTimeAlternative(day, p.Start, dur, cur, now, dayEnd, e.cfg); a != nil {
		return a
	}
	from, to := WindowHours(p.Start, p.End)
	minMean := max(e.cfg.BadBelow, cur.Mean+1)
	a, err := e.repo.NearbyAlternative(ctx, p.SpotID, from, to, cur.Hours, minMean, e.cfg)
	if err != nil {
		e.logger.WarnContext(ctx, "plan alternative: nearby failed", "plan", p.ID, "error", err)
	}
	if a != nil {
		return a
	}
	return &Alternative{Type: AltNone}
}

// Evaluate re-scores every upcoming plan, stores the result, queues the
// pushes Decide allows, then sends the queued pushes.
func (e *Evaluator) Evaluate(ctx context.Context) (EvalStats, error) {
	var st EvalStats
	now := e.now()
	list, err := e.repo.EvaluablePlans(ctx, now.Add(Horizon))
	if err != nil {
		return st, err
	}
	st.Plans = len(list)
	for _, p := range list {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		e.evaluateOne(ctx, p, now, &st)
	}
	e.dispatch(ctx, &st)
	return st, nil
}

func (e *Evaluator) evaluateOne(ctx context.Context, p EvalPlan, now time.Time, st *EvalStats) {
	loc, err := time.LoadLocation(p.TZ)
	if err != nil {
		e.logger.WarnContext(ctx, "plan tz invalid", "plan", p.ID, "tz", p.TZ)
		return
	}
	from, to := WindowHours(p.Start, p.End)
	hours, err := e.repo.WindowScores(ctx, p.SpotID, from, to)
	if err != nil {
		st.Failed++
		e.logger.ErrorContext(ctx, "plan scores failed", "plan", p.ID, "error", err)
		return
	}
	w, ok := Summarize(hours, e.cfg)
	if !ok {
		st.NoScores++
		return
	}
	baseline := p.Baseline
	if baseline == nil {
		b := w.Mean
		baseline = &b
	}
	state := Classify(w, baseline, e.cfg)
	var alt *Alternative
	if state != StateOK {
		alt = e.alternative(ctx, p, w, now, loc)
	}
	if err := e.repo.SaveEvaluation(ctx, p.ID, state, w, alt); err != nil {
		st.Failed++
		e.logger.ErrorContext(ctx, "plan evaluation save failed", "plan", p.ID, "error", err)
		return
	}
	act := Decide(p, state, now, loc, e.cfg)
	var kind string
	switch act {
	case ActDefer:
		st.Deferred++
		return
	case ActChange:
		kind = KindChanged
	case ActConfirm:
		kind = KindConfirmed
	default:
		return
	}
	n := Notification{
		Kind: kind, PlanID: p.ID, ShareToken: p.ShareToken, SpotID: p.SpotID,
		SpotName: p.SpotName, SpotKind: p.SpotKind, Start: p.Start.UTC(), End: p.End.UTC(), TZ: p.TZ,
		State: state, Score: w.Mean, CapReason: w.Cap, Alternative: alt,
	}
	claimed, queued, err := e.repo.ClaimNotification(ctx, n, localDate(now, loc))
	if err != nil {
		st.Failed++
		e.logger.ErrorContext(ctx, "plan notification claim failed", "plan", p.ID, "error", err)
		return
	}
	if !claimed {
		return
	}
	if kind == KindChanged {
		st.Changed++
	} else {
		st.Confirmed++
	}
	st.Queued += int(queued)
}

func (e *Evaluator) dispatch(ctx context.Context, st *EvalStats) {
	if err := e.repo.PrepareDeliveries(ctx, MaxDeliveryAttempts); err != nil {
		e.logger.WarnContext(ctx, "plan delivery sweep failed", "error", err)
	}
	if e.sender == nil {
		return
	}
	ds, err := e.repo.ClaimDeliveries(ctx, MaxDeliveryAttempts, deliveryBatch)
	if err != nil {
		st.Failed++
		e.logger.ErrorContext(ctx, "plan delivery claim failed", "error", err)
		return
	}
	for _, d := range ds {
		err := e.sender.SendPlan(ctx, d.DeviceID, d.Notification)
		switch {
		case err == nil:
			st.Sent++
			if merr := e.repo.MarkDeliverySent(ctx, d); merr != nil {
				e.logger.ErrorContext(ctx, "plan delivery sent-mark failed", "plan", d.PlanID, "error", merr)
			}
		case errors.Is(err, ErrNoPushToken):
			st.Dead++
			_ = e.repo.MarkDeliveryFailed(ctx, d, "dead", nil, err.Error())
		case errors.Is(err, ErrPushTokenDead):
			st.Dead++
			s := 410
			_ = e.repo.MarkDeliveryFailed(ctx, d, "dead", &s, err.Error())
		default:
			st.Failed++
			e.logger.ErrorContext(ctx, "plan push failed", "plan", d.PlanID, "device", d.DeviceID, "error", err)
			if merr := e.repo.MarkDeliveryFailed(ctx, d, "failed", nil, err.Error()); merr != nil {
				e.logger.ErrorContext(ctx, "plan delivery fail-mark failed", "plan", d.PlanID, "error", merr)
			}
		}
	}
}
