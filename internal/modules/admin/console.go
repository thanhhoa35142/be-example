package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPlanLimitNotFound = errors.New("plan limit not found")
	ErrInvalidPlanLimit  = errors.New("invalid plan limit")
	ErrCityPackNotFound  = errors.New("city pack not found")
	ErrSpotNotFound      = errors.New("spot not found")
	ErrInvalidTransition = errors.New("status change not allowed")
	ErrInvalidSpotAction = errors.New("invalid spot action")
	ErrInvalidSpotQuery  = errors.New("invalid spot query")
)

const (
	maxPlanLimit = 1000

	adminHiddenReason     = "hidden_by_admin"
	spotSearchDefault     = 50
	spotSearchMax         = 200
	spotSearchMinQueryLen = 2
)

type PlanLimit struct {
	Plan    string `json:"plan"`
	Feature string `json:"feature"`
	Limit   *int   `json:"limit"`
}

type CityPack struct {
	ID           uuid.UUID  `json:"id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Country      string     `json:"country"`
	Tz           string     `json:"tz"`
	Status       string     `json:"status"`
	LastImportAt *time.Time `json:"lastImportAt"`
	ActiveSpots  int64      `json:"activeSpots"`
	TotalSpots   int64      `json:"totalSpots"`

	NextStatuses []string `json:"nextStatuses"`
}

var packTransitions = map[string][]string{
	"draft":   {"beta", "retired"},
	"beta":    {"live", "draft", "retired"},
	"live":    {"beta", "retired"},
	"retired": {},
}

func nextPackStatuses(status string, imported bool) []string {
	out := []string{}
	for _, next := range packTransitions[status] {
		if (next == "beta" || next == "live") && !imported {
			continue
		}
		out = append(out, next)
	}
	return out
}

type SpotSearch struct {
	Query    string
	Status   string
	PackSlug string
	Limit    int
}

type SpotFilter struct {
	ID          *uuid.UUID
	NamePattern *string
	Status      *string
	PackSlug    *string
	Limit       int
}

type AdminSpot struct {
	ID             uuid.UUID  `json:"id"`
	Name           *string    `json:"name"`
	Kind           string     `json:"kind"`
	City           *string    `json:"city"`
	Country        string     `json:"country"`
	Status         string     `json:"status"`
	StatusReason   *string    `json:"statusReason"`
	Curated        bool       `json:"curated"`
	LastVerifiedAt *time.Time `json:"lastVerifiedAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	Lat            float64    `json:"lat"`
	Lon            float64    `json:"lon"`
	PackSlug       string     `json:"packSlug"`
	PackName       string     `json:"packName"`
}

var spotStatuses = map[string]bool{
	"pending": true, "active": true, "closed": true, "hidden": true, "merged": true,
}

func (s *Service) RequestJobRun(ctx context.Context, job string) (JobSchedule, error) {
	limits, ok := jobScheduleLimits[job]
	if !ok {
		return JobSchedule{}, ErrJobScheduleNotFound
	}
	schedule, err := s.repo.RequestJobRun(ctx, job)
	if err != nil {
		return JobSchedule{}, err
	}
	schedule.MinIntervalSeconds = limits.min
	schedule.MaxIntervalSeconds = limits.max
	return schedule, nil
}

func (s *Service) ClaimJobRunRequest(ctx context.Context, job string) (bool, error) {
	return s.repo.ClaimJobRunRequest(ctx, job)
}

func (s *Service) PlanLimits(ctx context.Context) ([]PlanLimit, error) {
	return s.repo.ListPlanLimits(ctx)
}

func (s *Service) UpdatePlanLimit(ctx context.Context, plan, feature string, limit int) (PlanLimit, error) {
	if limit < -1 || limit > maxPlanLimit {
		return PlanLimit{}, fmt.Errorf("%w: limit must be -1 (unlimited) or 0-%d", ErrInvalidPlanLimit, maxPlanLimit)
	}
	ok, err := s.repo.UpdatePlanLimit(ctx, plan, feature, limit)
	if err != nil {
		return PlanLimit{}, err
	}
	if !ok {
		return PlanLimit{}, ErrPlanLimitNotFound
	}
	return PlanLimit{Plan: plan, Feature: feature, Limit: &limit}, nil
}

func (s *Service) CityPacks(ctx context.Context) ([]CityPack, error) {
	packs, err := s.repo.ListCityPacks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range packs {
		packs[i].NextStatuses = nextPackStatuses(packs[i].Status, packs[i].LastImportAt != nil)
	}
	return packs, nil
}

func (s *Service) SetCityPackStatus(ctx context.Context, id uuid.UUID, status string) error {
	if _, ok := packTransitions[status]; !ok {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidTransition, status)
	}
	current, imported, err := s.repo.CityPackStatus(ctx, id)
	if err != nil {
		return err
	}
	if current == status {
		return nil
	}
	allowed := false
	for _, next := range nextPackStatuses(current, imported) {
		if next == status {
			allowed = true
			break
		}
	}
	if !allowed {
		if (status == "beta" || status == "live") && !imported {
			return fmt.Errorf("%w: a pack needs an import before it can be %s", ErrInvalidTransition, status)
		}
		return fmt.Errorf("%w: %s cannot become %s", ErrInvalidTransition, current, status)
	}
	return s.repo.UpdateCityPackStatus(ctx, id, status)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func normalizeSpotSearch(in SpotSearch) (SpotFilter, error) {
	f := SpotFilter{Limit: in.Limit}
	if f.Limit <= 0 || f.Limit > spotSearchMax {
		f.Limit = spotSearchDefault
	}
	q := strings.TrimSpace(in.Query)
	switch {
	case q == "":
	case len(q) > 100:
		return f, fmt.Errorf("%w: q is too long", ErrInvalidSpotQuery)
	default:
		if id, err := uuid.Parse(q); err == nil {
			f.ID = &id
		} else if len([]rune(q)) < spotSearchMinQueryLen {
			return f, fmt.Errorf("%w: q needs at least %d characters", ErrInvalidSpotQuery, spotSearchMinQueryLen)
		} else {
			pattern := "%" + escapeLike(q) + "%"
			f.NamePattern = &pattern
		}
	}
	if in.Status != "" {
		if !spotStatuses[in.Status] {
			return f, fmt.Errorf("%w: unknown status %q", ErrInvalidSpotQuery, in.Status)
		}
		status := in.Status
		f.Status = &status
	}
	if in.PackSlug != "" {
		slug := in.PackSlug
		f.PackSlug = &slug
	}
	if f.ID == nil && f.NamePattern == nil && f.Status == nil && f.PackSlug == nil {
		return f, fmt.Errorf("%w: give a name, a spot id, a status or a city", ErrInvalidSpotQuery)
	}
	return f, nil
}

func (s *Service) SearchSpots(ctx context.Context, in SpotSearch) ([]AdminSpot, error) {
	f, err := normalizeSpotSearch(in)
	if err != nil {
		return nil, err
	}
	return s.repo.SearchSpots(ctx, f)
}

var spotActions = map[string]struct {
	from   []string
	to     string
	reason *string
}{
	"hide":    {from: []string{"pending", "active", "closed"}, to: "hidden", reason: ptr(adminHiddenReason)},
	"restore": {from: []string{"hidden", "closed"}, to: "active", reason: nil},
}

func ptr[T any](v T) *T { return &v }

func (s *Service) SetSpotVisibility(ctx context.Context, id uuid.UUID, action string) (string, error) {
	a, ok := spotActions[action]
	if !ok {
		return "", ErrInvalidSpotAction
	}
	changed, err := s.repo.SetSpotStatus(ctx, id, a.from, a.to, a.reason)
	if err != nil {
		return "", err
	}
	if changed {
		return a.to, nil
	}
	current, err := s.repo.SpotStatus(ctx, id)
	if err != nil {
		return "", err
	}
	if current == a.to {
		return current, nil
	}
	return "", fmt.Errorf("%w: cannot %s a %s spot", ErrInvalidTransition, action, current)
}
