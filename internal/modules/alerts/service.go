package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
)

type Service struct {
	repo    Repository
	plans   PlanResolver
	sender  Sender
	maxRule func(plan string) int
}

func NewService(repo Repository, plans PlanResolver, sender Sender) *Service {
	return &Service{
		repo:   repo,
		plans:  plans,
		sender: sender,
		maxRule: func(plan string) int {
			if v, ok := Limits[plan]; ok {
				return v
			}
			return Limits["free"]
		},
	}
}

func (s *Service) List(ctx context.Context, deviceID uuid.UUID) ([]Rule, error) {
	return s.repo.ListRules(ctx, deviceID)
}

func (s *Service) Create(ctx context.Context, deviceID uuid.UUID, in RuleInput) (*Rule, error) {
	exists, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrDeviceInvalid
	}
	p, err := validate(in)
	if err != nil {
		return nil, err
	}
	if err := s.checkSpot(ctx, p.SpotID); err != nil {
		return nil, err
	}
	plan, err := s.plans.Plan(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	max := s.maxRule(plan)
	id, err := s.repo.InsertRuleLimited(ctx, deviceID, p.RuleInput, p.start, p.end, max)
	if errors.Is(err, ErrPlanLimit) {
		return nil, fmt.Errorf("%w: %d rule(s) on %s plan", ErrPlanLimit, max, plan)
	}
	if err != nil {
		return nil, err
	}
	return s.repo.GetRule(ctx, id)
}

func (s *Service) checkSpot(ctx context.Context, spotID *uuid.UUID) error {
	if spotID == nil {
		return nil
	}
	ok, err := s.repo.SpotActive(ctx, *spotID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSpotNotFound
	}
	return nil
}

func (s *Service) Update(ctx context.Context, id, deviceID uuid.UUID, in RuleInput) (*Rule, error) {
	p, err := validate(in)
	if err != nil {
		return nil, err
	}
	if err := s.checkSpot(ctx, p.SpotID); err != nil {
		return nil, err
	}
	ok, err := s.repo.UpdateRule(ctx, id, deviceID, p.RuleInput, p.start, p.end)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	return s.repo.GetRule(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id, deviceID uuid.UUID) error {
	ok, err := s.repo.DeleteRule(ctx, id, deviceID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Evaluate(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats

	if err := s.repo.SweepDeliveries(ctx); err != nil {
		slog.WarnContext(ctx, "alert delivery sweep failed", "error", err)
	}
	rules, err := s.repo.ListEnabledRules(ctx)
	if err != nil {
		return st, err
	}
	st.Rules = len(rules)
	tsHour := now.UTC().Truncate(time.Hour)

	for _, r := range rules {
		st.Evaluated++
		d, windowStart, ok := s.match(ctx, r, now, tsHour)
		if !ok {
			continue
		}

		_, _, _, hasToken, err := s.repo.DeviceAPNsToken(ctx, r.DeviceID)
		if err != nil {
			st.Failed++
			slog.ErrorContext(ctx, "alert token lookup failed", "rule", r.ID, "error", err)
			continue
		}
		if !hasToken {
			continue
		}
		claimed, err := s.repo.ClaimDelivery(ctx, *d, windowStart, MaxDeliveryAttempts)
		if err != nil {
			st.Failed++
			slog.ErrorContext(ctx, "alert delivery claim failed", "rule", r.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		st.Fired++
		if s.sender == nil {
			continue
		}
		switch err := s.sender.Send(ctx, *d); {
		case err == nil:
			st.Sent++
			if merr := s.repo.MarkDeliverySent(ctx, r.ID, windowStart, 200); merr != nil {
				slog.ErrorContext(ctx, "alert delivery sent-mark failed", "rule", r.ID, "error", merr)
			}
		case errors.Is(err, ErrNoPushToken):

			_ = s.repo.MarkDeliveryFailed(ctx, r.ID, windowStart, -1, err.Error())
		case errors.Is(err, ErrPushTokenDead):

			st.Failed++
			if uerr := s.repo.MarkDeviceUnregistered(ctx, r.DeviceID); uerr != nil {
				slog.ErrorContext(ctx, "device unregister failed", "device", r.DeviceID, "error", uerr)
			}
			_ = s.repo.MarkDeliveryFailed(ctx, r.ID, windowStart, 410, err.Error())
			_ = s.repo.MarkDeliveryDead(ctx, r.ID, windowStart, err.Error())
			slog.InfoContext(ctx, "alert device unregistered (APNs 410)", "rule", r.ID, "device", r.DeviceID)
		default:
			st.Failed++
			slog.ErrorContext(ctx, "alert send failed", "rule", r.ID, "device", r.DeviceID, "error", err)
			if merr := s.repo.MarkDeliveryFailed(ctx, r.ID, windowStart, -1, err.Error()); merr != nil {
				slog.ErrorContext(ctx, "alert delivery fail-mark failed", "rule", r.ID, "error", merr)
			}
		}
	}
	return st, nil
}

func (s *Service) match(ctx context.Context, r Rule, now, tsHour time.Time) (*Delivery, time.Time, bool) {
	loc, err := time.LoadLocation(r.TZ)
	if err != nil {
		return nil, time.Time{}, false
	}
	local := now.In(loc)
	dow := int(local.Weekday())
	if r.DaysMask&(1<<dow) == 0 {
		return nil, time.Time{}, false
	}

	tod := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute +
		time.Duration(local.Second())*time.Second
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	startTOD, endTOD := r.StartLocal, r.EndLocal

	var windowStart time.Time
	inWindow := false
	switch {
	case startTOD < endTOD:
		if tod >= startTOD && tod < endTOD {
			inWindow = true
			windowStart = dayStart.Add(startTOD)
		}
	case startTOD > endTOD:
		if tod >= startTOD {
			inWindow = true
			windowStart = dayStart.Add(startTOD)
		} else if tod < endTOD {
			inWindow = true
			windowStart = dayStart.Add(-24 * time.Hour).Add(startTOD)
		}
	default:
		return nil, time.Time{}, false
	}
	if !inWindow {
		return nil, time.Time{}, false
	}

	var d *Delivery
	if r.SpotID != nil {
		spot, err := s.repo.SpotScoreAt(ctx, *r.SpotID, tsHour)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				slog.ErrorContext(ctx, "alert spot score lookup failed", "spot", *r.SpotID, "error", err)
			}
			return nil, time.Time{}, false
		}
		if spot.Score < r.MinScore {
			return nil, time.Time{}, false
		}
		d = &Delivery{SpotID: spot.ID, SpotName: spot.Name, Score: spot.Score}
	} else if r.H3Cell != nil {
		lat, lon, err := geo.CellCenter(int64(*r.H3Cell))
		if err != nil {
			return nil, time.Time{}, false
		}
		radius := 5000
		if r.RadiusM != nil {
			radius = *r.RadiusM
		}
		near, err := s.repo.BestScoredSpotNear(ctx, lat, lon, radius, tsHour)
		if err != nil || near == nil || near.Score < r.MinScore {
			return nil, time.Time{}, false
		}
		d = near
	} else {
		return nil, time.Time{}, false
	}

	d.RuleID = r.ID
	d.DeviceID = r.DeviceID
	d.WindowStart = windowStart.UTC()
	return d, windowStart.UTC(), true
}
