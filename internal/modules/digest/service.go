package digest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

type Service struct {
	repo   Repository
	plans  PlanResolver
	sender Sender
	log    *slog.Logger
}

func NewService(repo Repository, plans PlanResolver, sender Sender, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, plans: plans, sender: sender, log: log}
}

// Get returns the device's digest or ErrNotFound.
func (s *Service) Get(ctx context.Context, deviceID uuid.UUID) (*Digest, error) {
	return s.repo.Get(ctx, deviceID)
}

// Delete removes the device's digest (idempotent).
func (s *Service) Delete(ctx context.Context, deviceID uuid.UUID) error {
	return s.repo.Delete(ctx, deviceID)
}

// Upsert creates/replaces the digest. Plus only. Spot ids are resolved
// (merged → target), de-duplicated and kept in request order.
func (s *Service) Upsert(ctx context.Context, deviceID uuid.UUID, spotIDs []uuid.UUID, enabled bool) (*Digest, error) {
	if len(spotIDs) > MaxSpots {
		return nil, fmt.Errorf("%w: at most %d spotIds", ErrInvalid, MaxSpots)
	}
	if len(spotIDs) == 0 && enabled {
		return nil, fmt.Errorf("%w: spotIds required when enabled", ErrInvalid)
	}
	ok, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrDeviceInvalid
	}
	plan, err := s.plans.Plan(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if plan != "plus" {
		return nil, fmt.Errorf("%w: %s plan", ErrPlanLimit, plan)
	}
	resolved, err := s.repo.ResolveSpots(ctx, spotIDs)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(spotIDs))
	seen := make(map[uuid.UUID]bool, len(spotIDs))
	for _, id := range spotIDs {
		target, ok := resolved[id]
		if !ok {
			return nil, fmt.Errorf("%w: unknown spot %s", ErrInvalid, id)
		}
		if !seen[target] {
			seen[target] = true
			out = append(out, target)
		}
	}
	return s.repo.Upsert(ctx, deviceID, out, enabled)
}

type scoreKey struct {
	spot uuid.UUID
	date time.Time
}

// RunWeekly sends the weekend digest to every due device. Per-device
// failures are logged and never abort the run.
func (s *Service) RunWeekly(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	cands, err := s.repo.ListCandidates(ctx)
	if err != nil {
		return st, err
	}
	st.Candidates = len(cands)

	type due struct {
		c     Candidate
		today time.Time
	}
	var dues []due
	need := map[uuid.UUID]bool{}
	for _, c := range cands {
		today, ok := IsSendTime(now, c.DeviceTZ, c.SendDow, c.SendHour, c.LastSentOn)
		if !ok {
			continue
		}
		dues = append(dues, due{c, today})
		for _, id := range c.SpotIDs {
			need[id] = true
		}
	}
	st.Due = len(dues)
	if len(dues) == 0 {
		return st, nil
	}

	ids := make([]uuid.UUID, 0, len(need))
	for id := range need {
		ids = append(ids, id)
	}
	spotRows, err := s.repo.Spots(ctx, ids)
	if err != nil {
		return st, err
	}
	spots := make(map[uuid.UUID]SpotInfo, len(spotRows))
	for _, sp := range spotRows {
		spots[sp.ID] = sp
	}
	cache := map[scoreKey][]HourScore{}

	for _, d := range dues {
		c := d.c
		log := s.log.With("device", c.DeviceID)
		plan, err := s.plans.Plan(ctx, c.DeviceID)
		if err != nil {
			log.Error("digest plan lookup failed", "error", err)
			st.Failed++
			continue
		}
		if plan != "plus" {
			st.Skipped++
			continue
		}
		claimed, err := s.repo.Claim(ctx, c.DeviceID, d.today, c.LastSentOn)
		if err != nil {
			log.Error("digest claim failed", "error", err)
			st.Failed++
			continue
		}
		if !claimed {
			st.Skipped++
			continue
		}

		del, ok, hadErr := s.best(ctx, log, c, d.today, spots, cache, now)
		if !ok {
			if hadErr {
				// Incomplete data: free the slot so a later run can retry.
				if rerr := s.repo.Release(ctx, c.DeviceID, d.today, c.LastSentOn); rerr != nil {
					log.Error("digest release failed", "error", rerr)
				}
				st.Failed++
				continue
			}
			// Nothing qualifies: stay silent, slot remains claimed.
			st.NoWindow++
			continue
		}
		err = s.sender.Send(ctx, del)
		switch {
		case err == nil:
			st.Sent++
		case errors.Is(err, alerts.ErrNoPushToken), errors.Is(err, alerts.ErrPushTokenDead):
			st.Skipped++
		default:
			log.Error("digest send failed", "error", err)
			if rerr := s.repo.Release(ctx, c.DeviceID, d.today, c.LastSentOn); rerr != nil {
				log.Error("digest release failed", "error", rerr)
			}
			st.Failed++
		}
	}
	return st, nil
}

// best finds the single best (spot, day, window) for the device.
func (s *Service) best(ctx context.Context, log *slog.Logger, c Candidate, today time.Time,
	spots map[uuid.UUID]SpotInfo, cache map[scoreKey][]HourScore, now time.Time) (Delivery, bool, bool) {

	var (
		bestDel Delivery
		bestWin Window
		found   bool
		hadErr  bool
	)
	for _, id := range c.SpotIDs {
		sp, ok := spots[id]
		if !ok {
			continue // merged away / deactivated since the digest was saved
		}
		loc, err := time.LoadLocation(sp.TZ)
		if err != nil {
			log.Warn("digest spot tz invalid", "spot", id, "tz", sp.TZ)
			continue
		}
		for _, date := range WeekendDates(today) {
			from, to := DayRange(date, loc)
			if !to.After(now) {
				continue
			}
			key := scoreKey{id, date}
			hours, cached := cache[key]
			if !cached {
				hours, err = s.repo.Scores(ctx, id, from, to)
				if err != nil {
					log.Error("digest scores failed", "spot", id, "error", err)
					hadErr = true
					continue
				}
				cache[key] = hours
			}
			w, ok := BestWindow(hours, MinHourScore, MinWindowHours, MaxWindowHours)
			if !ok {
				continue
			}
			if !found || w.Mean > bestWin.Mean ||
				(w.Mean == bestWin.Mean && w.Start.Before(bestWin.Start)) {
				found, bestWin = true, w
				bestDel = Delivery{
					DeviceID:    c.DeviceID,
					SpotID:      id,
					SpotName:    sp.Name,
					SpotTZ:      sp.TZ,
					WindowStart: w.Start.UTC(),
					WindowEnd:   w.End.UTC(),
					Score:       int(math.Round(w.Mean)),
				}
			}
		}
	}
	return bestDel, found, hadErr
}
