package getaways

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
)

type Service struct {
	repo Repository
	log  *slog.Logger
	cfg  Config
}

func NewService(repo Repository, log *slog.Logger, cfg Config) *Service {
	if log == nil {
		log = slog.Default()
	}
	if cfg.GoodScore <= 0 {
		cfg.GoodScore = DefaultGoodScore
	}
	if cfg.HintScore <= 0 {
		cfg.HintScore = DefaultHintScore
	}
	return &Service{repo: repo, log: log, cfg: cfg}
}

func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func dateKey(t time.Time) string { return t.Format("2006-01-02") }

// location loads a pack's time zone. A failure (unknown name, or a binary
// without tzdata) is returned, never papered over with UTC: the SQL side
// buckets hours by the pack's tz, so a UTC fallback here would misjudge the
// coverage of every day and silently empty the endpoint.
func location(tz string) (*time.Location, error) {
	l, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("getaways: time zone %q: %w", tz, err)
	}
	return l, nil
}

// DefaultStart is the date used when the client sends no start: the coming
// Saturday in UTC, or today when today is Saturday or Sunday in UTC.
func DefaultStart(now time.Time) time.Time {
	return digest.WeekendDates(civil(now.UTC()))[0]
}

// Refresh recomputes the stored city-hour series of every eligible pack for
// [now-1h, now+ForecastHorizon), drops local days without enough spot
// coverage, removes rows of ineligible packs and rows older than a day.
// A failing pack is logged and counted; the rest still refresh and the
// returned error joins the failures.
func (s *Service) Refresh(ctx context.Context, now time.Time) (RefreshStats, error) {
	var st RefreshStats
	hourNow := now.UTC().Truncate(time.Hour)
	from := hourNow.Add(-time.Hour)
	to := hourNow.Add(scoring.ForecastHorizon)

	caps, err := s.repo.ActiveCaps(ctx)
	if err != nil {
		return st, fmt.Errorf("getaways: scoring caps: %w", err)
	}
	packs, err := s.repo.Packs(ctx)
	if err != nil {
		return st, fmt.Errorf("getaways: packs: %w", err)
	}
	st.Packs = len(packs)

	var eligible []Pack
	keep := make([]uuid.UUID, 0, len(packs))
	for _, p := range packs {
		if p.ActiveSpots < MinPackSpots {
			st.Ineligible++
			s.log.Info("getaways: pack skipped, too few active spots", "pack", p.Slug, "spots", p.ActiveSpots, "min", MinPackSpots)
			continue
		}
		eligible = append(eligible, p)
		keep = append(keep, p.ID)
	}
	st.Eligible = len(eligible)

	if _, err := s.repo.DeleteExceptPacks(ctx, keep); err != nil {
		return st, fmt.Errorf("getaways: drop ineligible packs: %w", err)
	}

	var errs []error
	for _, p := range eligible {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		rows, dropped, err := s.computePack(ctx, p, caps, hourNow, from, to)
		if err == nil {
			err = s.repo.ReplaceHours(ctx, p.ID, from, rows, caps.Version, now.UTC())
		}
		if err != nil {
			st.Failed++
			s.log.Error("getaways: pack refresh failed", "pack", p.Slug, "error", err)
			errs = append(errs, fmt.Errorf("pack %s: %w", p.Slug, err))
			continue
		}
		st.Refreshed++
		st.Written += len(rows)
		st.DaysDropped += dropped
	}

	if _, err := s.repo.DeleteBefore(ctx, hourNow.Add(-24*time.Hour)); err != nil {
		errs = append(errs, fmt.Errorf("getaways: purge old rows: %w", err))
	}
	return st, errors.Join(errs...)
}

// computePack computes one pack's hours and applies the coverage rule (spec
// §3.3): a local day counts only if, for every hour 08-19 still to come,
// spots_n >= 0.8 x active spots. An hour with no row counts as zero coverage.
// Hours before now are not checked (they are never read), and neither are
// hours past the last scored hour (the day at the horizon edge is partial by
// nature). It returns the rows of the surviving days and how many days it
// dropped.
func (s *Service) computePack(ctx context.Context, p Pack, caps Caps, hourNow, from, to time.Time) ([]CityHour, int, error) {
	rows, err := s.repo.ComputeHours(ctx, p.ID, caps, from, to)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return nil, 0, nil
	}
	loc, err := location(p.TZ)
	if err != nil {
		return nil, 0, err
	}
	byHour := make(map[time.Time]CityHour, len(rows))
	last := rows[0].TSHour
	for _, r := range rows {
		byHour[r.TSHour.UTC()] = r
		if r.TSHour.After(last) {
			last = r.TSHour
		}
	}

	need := MinCoverage * float64(p.ActiveSpots)
	coverage := map[string]float64{}
	dates := map[string]time.Time{}
	for t := hourNow; !t.After(last) && t.Before(to); t = t.Add(time.Hour) {
		l := t.In(loc)
		if l.Hour() < FirstHour || l.Hour() > LastHour {
			continue
		}
		day := civil(l)
		k := dateKey(day)
		dates[k] = day
		c := 0.0
		if p.ActiveSpots > 0 {
			c = float64(byHour[t].SpotsN) / float64(p.ActiveSpots)
		}
		if cur, ok := coverage[k]; !ok || c < cur {
			coverage[k] = c
		}
	}

	drop := map[string]bool{}
	keys := make([]string, 0, len(coverage))
	for k := range coverage {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if coverage[k]*float64(p.ActiveSpots) < need-1e-9 {
			drop[k] = true
			s.log.Info("getaways: day skipped", "pack", p.Slug, "date", k, "coverage", math.Round(coverage[k]*1000)/1000)
		}
	}
	if len(drop) == 0 {
		return rows, 0, nil
	}
	kept := rows[:0:0]
	for _, r := range rows {
		if !drop[dateKey(civil(r.LocalDate))] {
			kept = append(kept, r)
		}
	}
	return kept, len(drop), nil
}

// ValidateQuery applies defaults and the §4 rules, returning the normalized
// query. start must satisfy todayUTC-1 <= start <= todayUTC+6; days 1-3 with
// the range ending by todayUTC+6; limit 1-39 (Free: at most 3).
func ValidateQuery(q Query, now time.Time) (Query, error) {
	today := civil(now.UTC())
	if q.Start.IsZero() {
		q.Start = DefaultStart(now)
	} else {
		q.Start = civil(q.Start)
	}
	if q.Start.Before(today.AddDate(0, 0, -1)) || q.Start.After(today.AddDate(0, 0, MaxStartOffsetDays)) {
		return q, ErrInvalidStart
	}
	if q.Days == 0 {
		q.Days = DefaultDays
	}
	if q.Days < 1 || q.Days > MaxDays ||
		q.Start.AddDate(0, 0, q.Days-1).After(today.AddDate(0, 0, MaxStartOffsetDays)) {
		return q, ErrInvalidRange
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit < 1 || q.Limit > MaxLimit {
		return q, ErrInvalidLimit
	}
	if q.Plan != PlanPlus && q.Limit > FreeLimit {
		q.Limit = FreeLimit
	}
	return q, nil
}

type dayEval struct {
	date      time.Time
	hours     []CityHour
	goodHours int
	window    *digest.Window
	score     int
	// counts is true when the day has a window and at least GoodDayMinHours
	// good hours; only such days count in windowDays.
	counts bool
}

type cityEval struct {
	rows       []OutlookRow
	days       []dayEval
	windowDays int
	best       *dayEval
}

// List returns the cities ranked by their weather on the requested local days,
// using only hours from now on.
func (s *Service) List(ctx context.Context, q Query, now time.Time) (*Result, error) {
	q, err := ValidateQuery(q, now)
	if err != nil {
		return nil, err
	}
	end := q.Start.AddDate(0, 0, q.Days-1)
	hourNow := now.UTC().Truncate(time.Hour)

	rows, err := s.repo.Outlook(ctx, q.Start, end, hourNow)
	if err != nil {
		return nil, err
	}

	byPack := map[uuid.UUID]*cityEval{}
	var order []uuid.UUID
	var computed time.Time
	for _, r := range rows {
		d := civil(r.LocalDate)
		if r.TSHour.Before(hourNow) || d.Before(q.Start) || d.After(end) {
			continue
		}
		ce := byPack[r.PackID]
		if ce == nil {
			ce = &cityEval{}
			byPack[r.PackID] = ce
			order = append(order, r.PackID)
		}
		ce.rows = append(ce.rows, r)
		if r.ComputedAt.After(computed) {
			computed = r.ComputedAt
		}
	}

	var cities []*cityEval
	for _, id := range order {
		ce := byPack[id]
		s.evalCity(ce, q.Start, q.Days)
		if ce.best != nil {
			cities = append(cities, ce)
		}
	}
	sort.SliceStable(cities, func(i, j int) bool {
		a, b := cities[i], cities[j]
		switch {
		case a.windowDays != b.windowDays:
			return a.windowDays > b.windowDays
		case a.best.score != b.best.score:
			return a.best.score > b.best.score
		case a.best.goodHours != b.best.goodHours:
			return a.best.goodHours > b.best.goodHours
		}
		return a.rows[0].Slug < b.rows[0].Slug
	})
	if len(cities) > q.Limit {
		cities = cities[:q.Limit]
	}

	res := &Result{Start: q.Start, Days: q.Days, Items: []Item{}}
	if !computed.IsZero() {
		c := computed.UTC()
		res.ComputedAt = &c
	}
	if len(cities) == 0 {
		return res, nil
	}
	if err := s.enrich(ctx, res, cities); err != nil {
		return nil, err
	}
	return res, nil
}

// evalCity computes each requested day's good hours and best window, the
// number of days that count (a window and >= GoodDayMinHours good hours) and
// the best day: highest window score, ties to more good hours, then the
// earlier date. Counting days win over days with a window only, so a 2-hour
// spike never headlines a city that also has a solid day.
func (s *Service) evalCity(ce *cityEval, start time.Time, days int) {
	byDate := map[string][]CityHour{}
	for _, r := range ce.rows {
		k := dateKey(civil(r.LocalDate))
		byDate[k] = append(byDate[k], r.CityHour)
	}
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i)
		hs := byDate[dateKey(date)]
		if len(hs) == 0 {
			continue
		}
		sort.Slice(hs, func(a, b int) bool { return hs[a].TSHour.Before(hs[b].TSHour) })
		scores := make([]digest.HourScore, 0, len(hs))
		good := 0
		for _, h := range hs {
			scores = append(scores, digest.HourScore{Start: h.TSHour, Score: h.WeatherScore})
			if h.WeatherScore >= s.cfg.GoodScore {
				good++
			}
		}
		de := dayEval{date: date, hours: hs, goodHours: good}
		if w, ok := digest.BestWindow(scores, s.cfg.GoodScore, WindowMinHours, WindowMaxHours); ok {
			de.window = &w
			de.score = int(math.Round(w.Mean))
			de.counts = good >= GoodDayMinHours
			if de.counts {
				ce.windowDays++
			}
		}
		ce.days = append(ce.days, de)
	}
	for i := range ce.days {
		d := &ce.days[i]
		if d.window == nil {
			continue
		}
		b := ce.best
		switch {
		case b == nil:
			ce.best = d
		case d.counts != b.counts:
			if d.counts {
				ce.best = d
			}
		case d.score > b.score || (d.score == b.score && d.goodHours > b.goodHours):
			ce.best = d
		}
	}
}

// enrich adds the center, top spot and reasons of the returned cities.
func (s *Service) enrich(ctx context.Context, res *Result, cities []*cityEval) error {
	packIDs := make([]uuid.UUID, 0, len(cities))
	cand := make([][]spotVote, len(cities))
	var spotIDs []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for i, ce := range cities {
		packIDs = append(packIDs, ce.rows[0].PackID)
		cand[i] = topSpotVotes(ce.best)
		for _, v := range cand[i] {
			if !seen[v.id] {
				seen[v.id] = true
				spotIDs = append(spotIDs, v.id)
			}
		}
	}
	centers, err := s.repo.Centers(ctx, packIDs)
	if err != nil {
		return err
	}
	var spotsByID map[uuid.UUID]SpotInfo
	if len(spotIDs) > 0 {
		if spotsByID, err = s.repo.Spots(ctx, spotIDs); err != nil {
			return err
		}
	}

	for i, ce := range cities {
		r0 := ce.rows[0]
		best := ce.best
		item := Item{
			Slug: r0.Slug, Name: r0.Name, Country: r0.Country, TZ: r0.TZ,
			WindowDays: ce.windowDays,
			BestDay:    best.date,
			Window:     Window{Start: best.window.Start.UTC(), End: best.window.End.UTC(), WeatherScore: best.score},
			GoodHours:  best.goodHours,
			DayScores:  make([]DayScore, 0, len(ce.days)),
			Reasons:    []Reason{},
		}
		if c, ok := centers[r0.PackID]; ok {
			item.Lon, item.Lat = c[0], c[1]
		}
		for _, d := range ce.days {
			ds := DayScore{Date: d.date, GoodHours: d.goodHours}
			if d.window != nil {
				sc := d.score
				ds.WeatherScore = &sc
			}
			item.DayScores = append(item.DayScores, ds)
		}

		top := pickTopSpot(cand[i], spotsByID)
		if top != nil {
			item.TopSpot = top
		}

		if dryDay(best.hours) {
			item.Reasons = append(item.Reasons, Reason{Code: ReasonDryDay, Params: map[string]any{}})
		}
		if hrs := int(best.window.End.Sub(best.window.Start) / time.Hour); hrs == WindowMaxHours {
			item.Reasons = append(item.Reasons, Reason{Code: ReasonLongWindow, Params: map[string]any{"hours": hrs}})
		}
		if len(item.Reasons) < 2 && top != nil {
			rs, err := s.repo.SpotReasons(ctx, top.ID, best.window.Start)
			if err != nil {
				return err
			}
			for _, r := range rs {
				if strings.HasPrefix(r.Code, "weather.") {
					if r.Params == nil {
						r.Params = map[string]any{}
					}
					item.Reasons = append(item.Reasons, r)
					break
				}
			}
		}
		res.Items = append(res.Items, item)
	}
	return nil
}

// dryDay reports whether no spot of the city has a rain cap in any of the
// day's remaining hours.
func dryDay(hours []CityHour) bool {
	if len(hours) == 0 {
		return false
	}
	for _, h := range hours {
		if h.RainShare > 0 {
			return false
		}
	}
	return true
}

type spotVote struct {
	id  uuid.UUID
	sum int
	// peak is the spot's highest hourly top-spot score inside the window.
	peak int
}

// topSpotVotes sums the top-spot score per spot over the window's hours and
// remembers each spot's best hourly score.
func topSpotVotes(d *dayEval) []spotVote {
	idx := map[uuid.UUID]int{}
	var out []spotVote
	for _, h := range d.hours {
		if h.TSHour.Before(d.window.Start) || !h.TSHour.Before(d.window.End) || h.TopSpotID == uuid.Nil {
			continue
		}
		i, ok := idx[h.TopSpotID]
		if !ok {
			i = len(out)
			idx[h.TopSpotID] = i
			out = append(out, spotVote{id: h.TopSpotID})
		}
		out[i].sum += h.TopSpotScore
		if h.TopSpotScore > out[i].peak {
			out[i].peak = h.TopSpotScore
		}
	}
	return out
}

// pickTopSpot takes the highest summed score among named spots (rows stored
// before the named-only rule may still point at an unnamed one); ties go to
// the spot name, then the id. Its score is the spot's best hourly score in
// the window. It returns nil when no candidate has a name.
func pickTopSpot(votes []spotVote, spots map[uuid.UUID]SpotInfo) *TopSpot {
	named := make([]spotVote, 0, len(votes))
	for _, v := range votes {
		if sp, ok := spots[v.id]; ok && sp.Name != nil && strings.TrimSpace(*sp.Name) != "" {
			named = append(named, v)
		}
	}
	if len(named) == 0 {
		return nil
	}
	sort.SliceStable(named, func(i, j int) bool {
		a, b := named[i], named[j]
		if a.sum != b.sum {
			return a.sum > b.sum
		}
		an, bn := *spots[a.id].Name, *spots[b.id].Name
		if an != bn {
			return an < bn
		}
		return a.id.String() < b.id.String()
	})
	w := named[0]
	return &TopSpot{ID: w.id, Name: spots[w.id].Name, Score: w.peak}
}
