package plans

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	Spot(ctx context.Context, id uuid.UUID) (*SpotRef, error)
	InsertLimited(ctx context.Context, p NewPlan, maxActive int) (uuid.UUID, error)
	ListForDevice(ctx context.Context, deviceID uuid.UUID) ([]Plan, error)
	Cancel(ctx context.Context, id, deviceID uuid.UUID) (bool, error)
	ActiveByToken(ctx context.Context, token string) (*Shared, error)
	Follow(ctx context.Context, token string, deviceID uuid.UUID, maxFollowers, maxFollows int) (uuid.UUID, error)
	Unfollow(ctx context.Context, token string, deviceID uuid.UUID) error
	WindowScores(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]HourScore, error)
}

// PlanResolver returns the device's entitlement plan ("free" | "plus").
type PlanResolver interface {
	Plan(ctx context.Context, deviceID uuid.UUID) (string, error)
}

type Service struct {
	repo  Repository
	plans PlanResolver
	cfg   Config
	now   func() time.Time
}

func NewService(repo Repository, plans PlanResolver, cfg Config) *Service {
	return &Service{repo: repo, plans: plans, cfg: cfg, now: time.Now}
}

func (s *Service) maxActive(ctx context.Context, deviceID uuid.UUID) (int, error) {
	if s.plans == nil {
		return Limits["free"], nil
	}
	p, err := s.plans.Plan(ctx, deviceID)
	if err != nil {
		return 0, err
	}
	if n, ok := Limits[p]; ok {
		return n, nil
	}
	return Limits["free"], nil
}

// Create validates the window, snapshots the baseline from the current
// scores (when there are any) and inserts under the active-plan cap.
func (s *Service) Create(ctx context.Context, deviceID uuid.UUID, in CreateInput) (*Plan, error) {
	spot, err := s.repo.Spot(ctx, in.SpotID)
	if err != nil {
		return nil, err
	}
	in, _, err = validate(in, spot.TZ, s.now())
	if err != nil {
		return nil, err
	}
	limit, err := s.maxActive(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	token, err := NewShareToken()
	if err != nil {
		return nil, err
	}
	np := NewPlan{ShareToken: token, DeviceID: deviceID, SpotID: spot.ID,
		Start: in.Start, End: in.End, TZ: in.TZ}

	from, to := WindowHours(in.Start, in.End)
	hours, err := s.repo.WindowScores(ctx, spot.ID, from, to)
	if err != nil {
		return nil, err
	}
	if w, ok := Summarize(hours, s.cfg); ok {
		// The baseline is the window as planned; the starting state is ok
		// unless the window is already bad (no "worse" against itself).
		mean := w.Mean
		st := Classify(w, &mean, s.cfg)
		np.Baseline, np.Score, np.State, np.CapReason = &mean, &mean, &st, w.Cap
	}
	id, err := s.repo.InsertLimited(ctx, np, limit)
	if err != nil {
		return nil, err
	}
	return s.find(ctx, deviceID, id)
}

func (s *Service) find(ctx context.Context, deviceID, id uuid.UUID) (*Plan, error) {
	list, err := s.repo.ListForDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// List returns owned and followed plans (role on each).
func (s *Service) List(ctx context.Context, deviceID uuid.UUID) ([]Plan, error) {
	return s.repo.ListForDevice(ctx, deviceID)
}

// Cancel marks an owned plan canceled (idempotent). ErrNotFound when the
// device does not own it.
func (s *Service) Cancel(ctx context.Context, id, deviceID uuid.UUID) error {
	ok, err := s.repo.Cancel(ctx, id, deviceID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// Follow subscribes deviceID to the plan behind token (idempotent).
func (s *Service) Follow(ctx context.Context, token string, deviceID uuid.UUID) (*Plan, error) {
	if !ValidToken(token) {
		return nil, ErrNotFound
	}
	id, err := s.repo.Follow(ctx, token, deviceID, MaxFollowers, MaxFollows)
	if err != nil {
		return nil, err
	}
	return s.find(ctx, deviceID, id)
}

// Unfollow is idempotent and never reveals whether the token exists.
func (s *Service) Unfollow(ctx context.Context, token string, deviceID uuid.UUID) error {
	if !ValidToken(token) {
		return nil
	}
	return s.repo.Unfollow(ctx, token, deviceID)
}

// Shared is the anonymous read by token: the live window score and state.
// Unknown, canceled and ended tokens are all ErrNotFound.
func (s *Service) Shared(ctx context.Context, token string) (*Shared, error) {
	if !ValidToken(token) {
		return nil, ErrNotFound
	}
	sh, baseline, err := s.sharedWithBaseline(ctx, token)
	if err != nil {
		return nil, err
	}
	from, to := WindowHours(sh.Start, sh.End)
	hours, err := s.repo.WindowScores(ctx, sh.SpotID, from, to)
	if err != nil {
		return nil, err
	}
	if w, ok := Summarize(hours, s.cfg); ok {
		sh.Window, sh.HasWindow = w, true
		if baseline == nil {
			baseline = &w.Mean
		}
		st := Classify(w, baseline, s.cfg)
		sh.State = &st
	}
	return sh, nil
}

// sharedBaseline is carried on Shared via the repository; kept separate so
// the baseline never leaves the service in the public view.
func (s *Service) sharedWithBaseline(ctx context.Context, token string) (*Shared, *int, error) {
	sh, err := s.repo.ActiveByToken(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	b := sh.baseline
	sh.baseline = nil
	return sh, b, nil
}
