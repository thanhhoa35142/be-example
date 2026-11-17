package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
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

// CityPackDetail embeds CityPack (id/slug/name/country/tz/status/counts/
// nextStatuses) plus the operational detail shown on the pack detail page.
type CityPackDetail struct {
	CityPack
	OsmExtract       *string     `json:"osmExtract"`
	ExtractFetchedAt *time.Time  `json:"extractFetchedAt"`
	PendingReports   int64       `json:"pendingReports"`
	SpotCounts       []NameCount `json:"spotCounts"`
	IngestRoles      []NameCount `json:"ingestRoles"`
	ExcludeReasons   []NameCount `json:"excludeReasons"`
	RecentRuns       []JobRun    `json:"recentRuns"`
	RecentSpots      []CitySpot  `json:"recentSpots"`
}

type CitySpot struct {
	ID        uuid.UUID `json:"id"`
	Name      *string   `json:"name"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
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

func (s *Service) RequestJobRunForPack(ctx context.Context, job, packSlug string) (JobSchedule, error) {
	limits, ok := jobScheduleLimits[job]
	if !ok {
		return JobSchedule{}, ErrJobScheduleNotFound
	}
	schedule, err := s.repo.RequestJobRunForPack(ctx, job, packSlug)
	if err != nil {
		return JobSchedule{}, err
	}
	schedule.MinIntervalSeconds = limits.min
	schedule.MaxIntervalSeconds = limits.max
	return schedule, nil
}

func (s *Service) ClaimJobRunRequest(ctx context.Context, job string) (string, bool, error) {
	return s.repo.ClaimJobRunRequest(ctx, job)
}

func (s *Service) PlanLimits(ctx context.Context) ([]PlanLimit, error) {
	return s.repo.ListPlanLimits(ctx)
}

// planCatalog mirrors the entitlement plans (t_plan_limits.plan CHECK allows
// the same set). featureCatalog lists the gated capabilities the API actually
// enforces — register new plan-gated features here or UpdatePlanLimit would
// accept rows nothing reads.
var planCatalog = map[string]bool{
	string(entitlement.PlanFree): true,
	string(entitlement.PlanPlus): true,
}

var featureCatalog = map[string]bool{
	"alert_rules": true,
}

func (s *Service) UpdatePlanLimit(ctx context.Context, plan, feature string, limit int) (PlanLimit, error) {
	if limit < -1 || limit > maxPlanLimit {
		return PlanLimit{}, fmt.Errorf("%w: limit must be -1 (unlimited) or 0-%d", ErrInvalidPlanLimit, maxPlanLimit)
	}
	if !planCatalog[plan] {
		return PlanLimit{}, fmt.Errorf("%w: unknown plan %q", ErrInvalidPlanLimit, plan)
	}
	if !featureCatalog[feature] {
		return PlanLimit{}, fmt.Errorf("%w: unknown feature %q", ErrInvalidPlanLimit, feature)
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

func (s *Service) CityPackDetail(ctx context.Context, id uuid.UUID) (CityPackDetail, error) {
	d, err := s.repo.CityPackDetail(ctx, id)
	if err != nil {
		return CityPackDetail{}, err
	}
	d.NextStatuses = nextPackStatuses(d.Status, d.LastImportAt != nil)
	return d, nil
}

// SetCityPackStatus reports whether the status actually changed so callers
// can skip cache invalidation on no-op requests.
func (s *Service) SetCityPackStatus(ctx context.Context, id uuid.UUID, status string) (bool, error) {
	if _, ok := packTransitions[status]; !ok {
		return false, fmt.Errorf("%w: unknown status %q", ErrInvalidTransition, status)
	}
	current, imported, err := s.repo.CityPackStatus(ctx, id)
	if err != nil {
		return false, err
	}
	if current == status {
		return false, nil
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
			return false, fmt.Errorf("%w: a pack needs an import before it can be %s", ErrInvalidTransition, status)
		}
		return false, fmt.Errorf("%w: %s cannot become %s", ErrInvalidTransition, current, status)
	}
	changed, err := s.repo.UpdateCityPackStatus(ctx, id, status)
	if err != nil {
		return false, err
	}
	return changed, nil
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
	"approve": {from: []string{"pending"}, to: "active", reason: ptr("approved_by_admin")},
}

func ptr[T any](v T) *T { return &v }

// SetSpotVisibility applies a status action and reports the resulting status
// plus whether a row actually changed (idempotent re-applies report false).
func (s *Service) SetSpotVisibility(ctx context.Context, id uuid.UUID, action string) (string, bool, error) {
	a, ok := spotActions[action]
	if !ok {
		return "", false, ErrInvalidSpotAction
	}
	changed, err := s.repo.SetSpotStatus(ctx, id, a.from, a.to, a.reason)
	if err != nil {
		return "", false, err
	}
	if changed {
		return a.to, true, nil
	}
	current, err := s.repo.SpotStatus(ctx, id)
	if err != nil {
		return "", false, err
	}
	if current == a.to {
		return current, false, nil
	}
	return "", false, fmt.Errorf("%w: cannot %s a %s spot", ErrInvalidTransition, action, current)
}

type SpotTransition struct {
	FromStatus *string   `json:"fromStatus"`
	ToStatus   string    `json:"toStatus"`
	ChangedAt  time.Time `json:"changedAt"`
}

type SpotScorePoint struct {
	TSHour     time.Time       `json:"tsHour"`
	Score      int             `json:"score"`
	Confidence int             `json:"confidence"`
	CapReason  *string         `json:"capReason"`
	Reasons    json.RawMessage `json:"reasons"`
	ComputedAt time.Time       `json:"computedAt"`
}

type SpotDetail struct {
	ID              uuid.UUID        `json:"id"`
	Name            *string          `json:"name"`
	Names           json.RawMessage  `json:"names"`
	Kind            string           `json:"kind"`
	City            *string          `json:"city"`
	Country         string           `json:"country"`
	Tz              string           `json:"tz"`
	CellID          int64            `json:"cellId"`
	Status          string           `json:"status"`
	StatusReason    *string          `json:"statusReason"`
	StatusChangedAt *time.Time       `json:"statusChangedAt"`
	Quality         *int             `json:"quality"`
	Attrs           json.RawMessage  `json:"attrs"`
	Curated         bool             `json:"curated"`
	Lat             float64          `json:"lat"`
	Lon             float64          `json:"lon"`
	Area            json.RawMessage  `json:"area"`
	MergedInto      *uuid.UUID       `json:"mergedInto"`
	MergedIntoName  *string          `json:"mergedIntoName"`
	PackSlug        string           `json:"packSlug"`
	PackName        string           `json:"packName"`
	LastVerifiedAt  *time.Time       `json:"lastVerifiedAt"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
	Amenities       []spots.Amenity  `json:"amenities"`
	Sources         []spots.Source   `json:"sources"`
	Scores          []SpotScorePoint `json:"scores"`
	Transitions     []SpotTransition `json:"transitions"`
	AuditEvents     []AuditEvent     `json:"auditEvents"`
}

func (s *Service) SpotDetail(ctx context.Context, id uuid.UUID) (*SpotDetail, error) {
	return s.repo.SpotDetail(ctx, id)
}
