package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
)

func (r *AdminRepo) RequestJobRun(ctx context.Context, job string) (admin.JobSchedule, error) {
	row, err := r.q.RequestJobRun(ctx, job)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.JobSchedule{}, admin.ErrJobScheduleNotFound
	}
	if err != nil {
		return admin.JobSchedule{}, err
	}
	return jobScheduleFromRow(row), nil
}

func (r *AdminRepo) ClaimJobRunRequest(ctx context.Context, job string) (bool, error) {
	n, err := r.q.ClaimJobRunRequest(ctx, job)
	return n > 0, err
}

func (r *AdminRepo) ListPlanLimits(ctx context.Context) ([]admin.PlanLimit, error) {
	rows, err := r.q.ListPlanLimits(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.PlanLimit, 0, len(rows))
	for _, row := range rows {
		l := admin.PlanLimit{Plan: row.Plan, Feature: row.Feature}
		if row.LimitValue.Valid {
			v := int(row.LimitValue.Int32)
			l.Limit = &v
		}
		out = append(out, l)
	}
	return out, nil
}

func (r *AdminRepo) UpdatePlanLimit(ctx context.Context, plan, feature string, limit int) (bool, error) {
	n, err := r.q.UpdatePlanLimit(ctx, UpdatePlanLimitParams{
		Plan:       plan,
		Feature:    feature,
		LimitValue: pgtype.Int4{Int32: int32(limit), Valid: true},
	})
	return n > 0, err
}

func (r *AdminRepo) ListCityPacks(ctx context.Context) ([]admin.CityPack, error) {
	rows, err := r.q.ListAdminCityPacks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.CityPack, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.CityPack{
			ID:           row.ID,
			Slug:         row.Slug,
			Name:         row.Name,
			Country:      row.Country,
			Tz:           row.Tz,
			Status:       row.Status,
			LastImportAt: timePtr(row.LastImportAt),
			ActiveSpots:  row.ActiveSpots,
			TotalSpots:   row.TotalSpots,
		})
	}
	return out, nil
}

func (r *AdminRepo) CityPackStatus(ctx context.Context, id uuid.UUID) (string, bool, error) {
	row, err := r.q.GetCityPackStatus(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, admin.ErrCityPackNotFound
	}
	if err != nil {
		return "", false, err
	}
	return row.Status, row.LastImportAt.Valid, nil
}

func (r *AdminRepo) UpdateCityPackStatus(ctx context.Context, id uuid.UUID, status string) error {
	n, err := r.q.UpdateCityPackStatus(ctx, UpdateCityPackStatusParams{ID: id, Status: status})
	if err != nil {
		return mapTransitionError(err)
	}
	if n == 0 {
		return admin.ErrCityPackNotFound
	}
	return nil
}

func (r *AdminRepo) SearchSpots(ctx context.Context, f admin.SpotFilter) ([]admin.AdminSpot, error) {
	params := SearchAdminSpotsParams{
		Q:        textParam(f.NamePattern),
		Status:   textParam(f.Status),
		PackSlug: textParam(f.PackSlug),
		RowLimit: int32(f.Limit),
	}
	if f.ID != nil {
		params.ID = pgtype.UUID{Bytes: *f.ID, Valid: true}
	}
	rows, err := r.q.SearchAdminSpots(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]admin.AdminSpot, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.AdminSpot{
			ID:             row.ID,
			Name:           textPtr(row.Name),
			Kind:           row.Kind,
			City:           textPtr(row.City),
			Country:        row.Country,
			Status:         row.Status,
			StatusReason:   textPtr(row.StatusReason),
			Curated:        row.Curated,
			LastVerifiedAt: timePtr(row.LastVerifiedAt),
			UpdatedAt:      row.UpdatedAt.UTC(),
			Lat:            row.Lat,
			Lon:            row.Lon,
			PackSlug:       row.PackSlug,
			PackName:       row.PackName,
		})
	}
	return out, nil
}

func (r *AdminRepo) SpotStatus(ctx context.Context, id uuid.UUID) (string, error) {
	status, err := r.q.GetSpotStatus(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", admin.ErrSpotNotFound
	}
	return status, err
}

func (r *AdminRepo) SetSpotStatus(ctx context.Context, id uuid.UUID, from []string, status string, reason *string) (bool, error) {
	n, err := r.q.AdminSetSpotStatus(ctx, AdminSetSpotStatusParams{
		ID:           id,
		Status:       status,
		Reason:       textParam(reason),
		FromStatuses: from,
	})
	if err != nil {
		return false, mapTransitionError(err)
	}
	return n > 0, nil
}

func mapTransitionError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" {
		return errors.Join(admin.ErrInvalidTransition, err)
	}
	return err
}
