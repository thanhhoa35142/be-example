package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
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

func (r *AdminRepo) RequestJobRunForPack(ctx context.Context, job, packSlug string) (admin.JobSchedule, error) {
	row, err := r.q.RequestJobRunForPack(ctx, RequestJobRunForPackParams{
		Job:              job,
		RunRequestedPack: pgtype.Text{String: packSlug, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.JobSchedule{}, admin.ErrJobScheduleNotFound
	}
	if err != nil {
		return admin.JobSchedule{}, err
	}
	return jobScheduleFromRow(row), nil
}

func (r *AdminRepo) ClaimJobRunRequest(ctx context.Context, job string) (string, bool, error) {
	pack, err := r.q.ClaimJobRunRequest(ctx, job)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return pack.String, true, nil
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

var citySpotStatusOrder = []string{"pending", "active", "closed", "hidden", "merged"}

func (r *AdminRepo) CityPackDetail(ctx context.Context, id uuid.UUID) (admin.CityPackDetail, error) {
	row, err := r.q.GetAdminCityPack(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.CityPackDetail{}, admin.ErrCityPackNotFound
	}
	if err != nil {
		return admin.CityPackDetail{}, err
	}

	spotRows, err := r.q.CountAdminCityPackSpotsByStatus(ctx, id)
	if err != nil {
		return admin.CityPackDetail{}, err
	}
	roleRows, err := r.q.CountAdminCityPackIngestFeatures(ctx, id)
	if err != nil {
		return admin.CityPackDetail{}, err
	}
	reasonRows, err := r.q.CountAdminCityPackExcludeReasons(ctx, id)
	if err != nil {
		return admin.CityPackDetail{}, err
	}
	pendingReports, err := r.q.CountAdminCityPackPendingReports(ctx, id)
	if err != nil {
		return admin.CityPackDetail{}, err
	}
	runRows, err := r.q.ListAdminCityPackJobRuns(ctx, ListAdminCityPackJobRunsParams{
		CityPackID: pgtype.UUID{Bytes: id, Valid: true},
		Limit:      10,
	})
	if err != nil {
		return admin.CityPackDetail{}, err
	}
	spotList, err := r.q.ListAdminCityPackSpots(ctx, ListAdminCityPackSpotsParams{
		CityPackID: id,
		Limit:      10,
	})
	if err != nil {
		return admin.CityPackDetail{}, err
	}

	d := admin.CityPackDetail{
		CityPack: admin.CityPack{
			ID:           row.ID,
			Slug:         row.Slug,
			Name:         row.Name,
			Country:      row.Country,
			Tz:           row.Tz,
			Status:       row.Status,
			LastImportAt: timePtr(row.LastImportAt),
		},
		OsmExtract:       textPtr(row.OsmExtract),
		ExtractFetchedAt: timePtr(row.ExtractFetchedAt),
		PendingReports:   pendingReports,
		SpotCounts:       []admin.NameCount{},
		IngestRoles:      []admin.NameCount{},
		ExcludeReasons:   []admin.NameCount{},
		RecentRuns:       make([]admin.JobRun, 0, len(runRows)),
		RecentSpots:      make([]admin.CitySpot, 0, len(spotList)),
	}

	spotCounts := make([]admin.NameCount, 0, len(spotRows))
	for _, sr := range spotRows {
		spotCounts = append(spotCounts, admin.NameCount{Name: sr.Status, Count: sr.Count})
		d.TotalSpots += sr.Count
		if sr.Status == "active" {
			d.ActiveSpots = sr.Count
		}
	}
	sort.SliceStable(spotCounts, func(i, j int) bool {
		return citySpotStatusRank(spotCounts[i].Name) < citySpotStatusRank(spotCounts[j].Name)
	})
	d.SpotCounts = spotCounts

	for _, rr := range roleRows {
		d.IngestRoles = append(d.IngestRoles, admin.NameCount{Name: rr.Role, Count: rr.Count})
	}
	for _, rr := range reasonRows {
		d.ExcludeReasons = append(d.ExcludeReasons, admin.NameCount{
			Name:  rr.ExcludeReason.String,
			Count: rr.Count,
		})
	}
	for _, run := range runRows {
		d.RecentRuns = append(d.RecentRuns, jobRunFromModel(run))
	}
	for _, s := range spotList {
		d.RecentSpots = append(d.RecentSpots, admin.CitySpot{
			ID:        s.ID,
			Name:      textPtr(s.Name),
			Kind:      s.Kind,
			Status:    s.Status,
			UpdatedAt: s.UpdatedAt.UTC(),
		})
	}
	return d, nil
}

func citySpotStatusRank(status string) int {
	for i, s := range citySpotStatusOrder {
		if s == status {
			return i
		}
	}
	return len(citySpotStatusOrder)
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

func (r *AdminRepo) UpdateCityPackStatus(ctx context.Context, id uuid.UUID, status string) (bool, error) {
	n, err := r.q.UpdateCityPackStatus(ctx, UpdateCityPackStatusParams{ID: id, Status: status})
	if err != nil {
		return false, mapTransitionError(err)
	}
	if n == 0 {
		return false, admin.ErrCityPackNotFound
	}
	return true, nil
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

const (
	spotDetailScoresLimit      = 48
	spotDetailTransitionsLimit = 100
	spotDetailAuditLimit       = 50
)

func (r *AdminRepo) SpotDetail(ctx context.Context, id uuid.UUID) (*admin.SpotDetail, error) {
	row, err := r.q.GetAdminSpotByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, admin.ErrSpotNotFound
	}
	if err != nil {
		return nil, err
	}

	d := &admin.SpotDetail{
		ID:              row.ID,
		Name:            textPtr(row.Name),
		Names:           json.RawMessage(row.Names),
		Kind:            row.Kind,
		City:            textPtr(row.City),
		Country:         row.Country,
		Tz:              row.Tz,
		CellID:          row.CellID,
		Status:          row.Status,
		StatusReason:    textPtr(row.StatusReason),
		StatusChangedAt: timePtr(row.StatusChangedAt),
		Quality:         int2Ptr(row.Quality),
		Attrs:           json.RawMessage(row.Attrs),
		Curated:         row.Curated,
		Lat:             row.Lat,
		Lon:             row.Lon,
		Area:            json.RawMessage(row.AreaGeojson),
		MergedInto:      uuidPtr(row.MergedInto),
		MergedIntoName:  textPtr(row.MergedIntoName),
		PackSlug:        row.PackSlug,
		PackName:        row.PackName,
		LastVerifiedAt:  timePtr(row.LastVerifiedAt),
		CreatedAt:       row.CreatedAt.UTC(),
		UpdatedAt:       row.UpdatedAt.UTC(),
	}

	amenities, err := r.q.ListSpotAmenities(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Amenities = make([]spots.Amenity, 0, len(amenities))
	for _, a := range amenities {
		d.Amenities = append(d.Amenities, spots.Amenity{Type: a.Type, DistanceM: int(a.DistanceM), Source: a.Source})
	}

	sources, err := r.q.ListSpotSources(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Sources = make([]spots.Source, 0, len(sources))
	for _, s := range sources {
		d.Sources = append(d.Sources, spots.Source{
			Name:       s.Source,
			ID:         s.SourceID,
			Version:    intPtr(s.Version),
			ImportedAt: s.ImportedAt.UTC(),
		})
	}

	scores, err := r.q.ListAdminSpotScores(ctx, ListAdminSpotScoresParams{SpotID: id, Limit: spotDetailScoresLimit})
	if err != nil {
		return nil, err
	}
	d.Scores = make([]admin.SpotScorePoint, 0, len(scores))
	for _, s := range scores {
		d.Scores = append(d.Scores, admin.SpotScorePoint{
			TSHour:     s.TsHour.UTC(),
			Score:      int(s.Score),
			Confidence: int(s.Confidence),
			CapReason:  textPtr(s.CapReason),
			Reasons:    json.RawMessage(s.Reasons),
			ComputedAt: s.ComputedAt.UTC(),
		})
	}

	transitions, err := r.q.ListAdminSpotTransitions(ctx, ListAdminSpotTransitionsParams{
		EntityID: id.String(), Limit: spotDetailTransitionsLimit,
	})
	if err != nil {
		return nil, err
	}
	d.Transitions = make([]admin.SpotTransition, 0, len(transitions))
	for _, t := range transitions {
		d.Transitions = append(d.Transitions, admin.SpotTransition{
			FromStatus: textPtr(t.FromStatus),
			ToStatus:   t.ToStatus,
			ChangedAt:  t.ChangedAt.UTC(),
		})
	}

	auditPath := "/v1/admin/spots/" + id.String()
	events, err := r.q.ListAdminSpotAuditEvents(ctx, ListAdminSpotAuditEventsParams{Path: auditPath, Limit: spotDetailAuditLimit})
	if err != nil {
		return nil, err
	}
	d.AuditEvents = make([]admin.AuditEvent, 0, len(events))
	for _, e := range events {
		var uid, email, reqID *string
		if e.AdminUserID.Valid {
			s := uuid.UUID(e.AdminUserID.Bytes).String()
			uid = &s
		}
		if e.Email.Valid {
			s := e.Email.String
			email = &s
		}
		if e.RequestID.Valid {
			s := e.RequestID.String
			reqID = &s
		}
		d.AuditEvents = append(d.AuditEvents, admin.AuditEvent{
			ID:          e.ID,
			AdminUserID: uid,
			Email:       email,
			AuthMethod:  e.AuthMethod,
			Method:      e.Method,
			Path:        e.Path,
			Status:      int(e.Status),
			ClientIP:    e.ClientIp,
			RequestID:   reqID,
			CreatedAt:   e.CreatedAt.UTC(),
		})
	}

	return d, nil
}
