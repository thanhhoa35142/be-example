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

// List returns the device's rules.
func (s *Service) List(ctx context.Context, deviceID uuid.UUID) ([]Rule, error) {
	return s.repo.ListRules(ctx, deviceID)
}

// Create validates the rule and enforces the plan limit (Free = 1 area).
func (s *Service) Create(ctx context.Context, deviceID uuid.UUID, in RuleInput) (*Rule, error) {
	exists, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrDeviceInvalid
	}
	n, err := normalize(in)
	if err != nil {
		return nil, err
	}
	plan, err := s.plans.Plan(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	max := s.maxRule(plan)
	if max >= 0 {
		count, err := s.repo.CountRules(ctx, deviceID)
		if err != nil {
			return nil, err
		}
		if count >= max {
			return nil, fmt.Errorf("%w: %d rule(s) on %s plan", ErrPlanLimit, max, plan)
		}
	}
	start, _ := parseHHMM(n.StartLocal)
	end, _ := parseHHMM(n.EndLocal)
	id, err := s.repo.InsertRule(ctx, deviceID, n, start, end)
	if err != nil {
		return nil, err
	}
	return s.repo.GetRule(ctx, id)
}

// Update replaces a rule owned by the device.
func (s *Service) Update(ctx context.Context, id, deviceID uuid.UUID, in RuleInput) (*Rule, error) {
	n, err := normalize(in)
	if err != nil {
		return nil, err
	}
	start, _ := parseHHMM(n.StartLocal)
	end, _ := parseHHMM(n.EndLocal)
	ok, err := s.repo.UpdateRule(ctx, id, deviceID, n, start, end)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	return s.repo.GetRule(ctx, id)
}

// Delete removes a rule owned by the device.
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

// Evaluate scans enabled rules and fires deliveries for windows where the
// best matching spot score meets min_score. Each (rule, window) fires at most
// once thanks to the alert_delivery primary key.
func (s *Service) Evaluate(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
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
		claimed, err := s.repo.RecordDelivery(ctx, *d, windowStart, -1)
		if err != nil {
			st.Failed++
			slog.ErrorContext(ctx, "alert delivery record failed", "rule", r.ID, "error", err)
			continue
		}
		if !claimed {
			continue // already sent for this window
		}
		st.Fired++
		if s.sender == nil {
			continue
		}
		if err := s.sender.Send(ctx, *d); err != nil {
			if errors.Is(err, ErrNoPushToken) {
				continue // device never registered push — skip silently
			}
			st.Failed++
			slog.ErrorContext(ctx, "alert send failed", "rule", r.ID, "device", r.DeviceID, "error", err)
		} else {
			st.Sent++
			_ = s.repo.SetDeliveryStatus(ctx, r.ID, windowStart, 200)
		}
	}
	return st, nil
}

// match returns the delivery for a rule if the current local time is inside an
// enabled window and the best spot score meets min_score.
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

	// Local time-of-day and window bookkeeping (windows may cross midnight).
	tod := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute +
		time.Duration(local.Second())*time.Second
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	startTOD, endTOD := r.StartLocal, r.EndLocal

	var windowStart time.Time
	inWindow := false
	switch {
	case startTOD < endTOD: // same-day window
		if tod >= startTOD && tod < endTOD {
			inWindow = true
			windowStart = dayStart.Add(startTOD)
		}
	case startTOD > endTOD: // overnight window
		if tod >= startTOD {
			inWindow = true
			windowStart = dayStart.Add(startTOD)
		} else if tod < endTOD {
			inWindow = true
			windowStart = dayStart.Add(-24 * time.Hour).Add(startTOD)
		}
	default:
		return nil, time.Time{}, false // zero-length window
	}
	if !inWindow {
		return nil, time.Time{}, false
	}

	// Find best spot score for this rule's area.
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
