package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
)

// cursorParams maps the shared (created_at, id) keyset cursor onto the
// before_ts/before_id param pair used by the DESC ops lists.
func cursorParams(before *admin.ReportCursor) (pgtype.Timestamptz, pgtype.UUID) {
	if before == nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}
	}
	return pgtype.Timestamptz{Time: before.CreatedAt, Valid: true},
		pgtype.UUID{Bytes: before.ID, Valid: true}
}

func rangeParams(from, to time.Time) (pgtype.Timestamptz, pgtype.Timestamptz) {
	var f, t pgtype.Timestamptz
	if !from.IsZero() {
		f = pgtype.Timestamptz{Time: from, Valid: true}
	}
	if !to.IsZero() {
		t = pgtype.Timestamptz{Time: to, Valid: true}
	}
	return f, t
}

func smallintParam(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

func bigintParam(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func textParamOpt(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func spotReportFromRow(row ListSpotReportsAdminRow) admin.SpotReport {
	return admin.SpotReport{
		ID:         row.ID,
		SpotID:     row.SpotID,
		SpotName:   row.SpotName.String,
		DeviceID:   row.DeviceID,
		Type:       row.Type,
		IssueCode:  textPtr(row.IssueCode),
		Status:     row.Status,
		ReviewedAt: timePtr(row.ReviewedAt),
		CreatedAt:  row.CreatedAt.UTC(),
	}
}

func (r *AdminRepo) ListSpotReports(ctx context.Context, f admin.SpotReportFilter, limit int, before *admin.ReportCursor) ([]admin.SpotReport, error) {
	beforeTs, beforeID := cursorParams(before)
	fromTs, toTs := rangeParams(f.From, f.To)
	rows, err := r.q.ListSpotReportsAdmin(ctx, ListSpotReportsAdminParams{
		Limit:    int32(limit),
		Status:   textParamOpt(f.Status),
		Type:     textParamOpt(f.Type),
		SpotID:   uuidParamOpt(f.SpotID),
		FromTs:   fromTs,
		ToTs:     toTs,
		BeforeTs: beforeTs,
		BeforeID: beforeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.SpotReport, 0, len(rows))
	for _, row := range rows {
		out = append(out, spotReportFromRow(row))
	}
	return out, nil
}

func (r *AdminRepo) GetSpotReport(ctx context.Context, id uuid.UUID) (*admin.SpotReport, error) {
	row, err := r.q.GetSpotReportAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, admin.ErrReportNotFound
	}
	if err != nil {
		return nil, err
	}
	rep := spotReportFromRow(ListSpotReportsAdminRow(row))
	return &rep, nil
}

func (r *AdminRepo) ListCrowdReports(ctx context.Context, f admin.CrowdReportFilter, limit int, before *admin.ReportCursor) ([]admin.CrowdReport, error) {
	beforeTs, beforeID := cursorParams(before)
	fromTs, toTs := rangeParams(f.From, f.To)
	rows, err := r.q.ListCrowdReportsAdmin(ctx, ListCrowdReportsAdminParams{
		Limit:    int32(limit),
		SpotID:   uuidParamOpt(f.SpotID),
		DeviceID: uuidParamOpt(f.DeviceID),
		Level:    smallintParam(f.Level),
		FromTs:   fromTs,
		ToTs:     toTs,
		BeforeTs: beforeTs,
		BeforeID: beforeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.CrowdReport, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.CrowdReport{
			ID:        row.ID,
			SpotID:    row.SpotID,
			SpotName:  row.SpotName.String,
			DeviceID:  row.DeviceID,
			Level:     int(row.Level),
			LocalDow:  int(row.LocalDow),
			LocalHour: int(row.LocalHour),
			CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return out, nil
}

func (r *AdminRepo) ListScoreFeedbacks(ctx context.Context, f admin.ScoreFeedbackFilter, limit int, before *admin.ReportCursor) ([]admin.ScoreFeedback, error) {
	beforeTs, beforeID := cursorParams(before)
	fromTs, toTs := rangeParams(f.From, f.To)
	rows, err := r.q.ListScoreFeedbacksAdmin(ctx, ListScoreFeedbacksAdminParams{
		Limit:    int32(limit),
		SpotID:   uuidParamOpt(f.SpotID),
		DeviceID: uuidParamOpt(f.DeviceID),
		Verdict:  smallintParam(f.Verdict),
		FromTs:   fromTs,
		ToTs:     toTs,
		BeforeTs: beforeTs,
		BeforeID: beforeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.ScoreFeedback, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.ScoreFeedback{
			ID:        row.ID,
			SpotID:    row.SpotID,
			SpotName:  row.SpotName.String,
			TSHour:    row.TsHour.UTC(),
			DeviceID:  row.DeviceID,
			Verdict:   int(row.Verdict),
			CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return out, nil
}

func deviceFromRow(row ListAdminDevicesRow) admin.AdminDevice {
	return admin.AdminDevice{
		ID:         row.ID,
		APNsEnv:    textPtr(row.ApnsEnv),
		PushStatus: row.PushStatus,
		Locale:     textPtr(row.Locale),
		TZ:         textPtr(row.Tz),
		AppVersion: textPtr(row.AppVersion),
		CreatedAt:  row.CreatedAt.UTC(),
		LastSeenAt: timePtr(row.LastSeenAt),
		DeletedAt:  timePtr(row.DeletedAt),
		LinkedSubs: row.Subscriptions,
	}
}

func (r *AdminRepo) ListDevices(ctx context.Context, f admin.AdminDeviceFilter, limit int, before *admin.ReportCursor) ([]admin.AdminDevice, error) {
	beforeTs, beforeID := cursorParams(before)
	rows, err := r.q.ListAdminDevices(ctx, ListAdminDevicesParams{
		Limit:          int32(limit),
		ID:             uuidParamOpt(f.ID),
		PushStatus:     textParamOpt(f.PushStatus),
		IncludeDeleted: f.IncludeDeleted,
		BeforeTs:       beforeTs,
		BeforeID:       beforeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.AdminDevice, 0, len(rows))
	for _, row := range rows {
		out = append(out, deviceFromRow(row))
	}
	return out, nil
}

func (r *AdminRepo) GetDevice(ctx context.Context, id uuid.UUID) (*admin.AdminDeviceDetail, error) {
	row, err := r.q.GetAdminDevice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, admin.ErrDeviceNotFound
	}
	if err != nil {
		return nil, err
	}
	prefs := map[string]any{}
	if len(row.Prefs) > 0 {
		_ = json.Unmarshal(row.Prefs, &prefs)
	}
	return &admin.AdminDeviceDetail{
		AdminDevice: admin.AdminDevice{
			ID:         row.ID,
			APNsEnv:    textPtr(row.ApnsEnv),
			PushStatus: row.PushStatus,
			Locale:     textPtr(row.Locale),
			TZ:         textPtr(row.Tz),
			AppVersion: textPtr(row.AppVersion),
			CreatedAt:  row.CreatedAt.UTC(),
			LastSeenAt: timePtr(row.LastSeenAt),
			DeletedAt:  timePtr(row.DeletedAt),
		},
		Prefs:         prefs,
		Subscriptions: []admin.DeviceSubscription{},
	}, nil
}

func (r *AdminRepo) ListDeviceSubscriptions(ctx context.Context, deviceID uuid.UUID) ([]admin.DeviceSubscription, error) {
	rows, err := r.q.ListAdminDeviceSubscriptions(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]admin.DeviceSubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.DeviceSubscription{
			OriginalTransactionID: row.OriginalTransactionID,
			ProductID:             row.ProductID,
			Status:                row.Status,
			Environment:           row.Environment,
			Ownership:             row.Ownership,
			IsTrial:               row.IsTrial,
			AutoRenew:             row.AutoRenew,
			ExpiresAt:             timePtr(row.ExpiresAt),
			GraceExpiresAt:        timePtr(row.GraceExpiresAt),
			RevokedAt:             timePtr(row.RevokedAt),
			LinkedAt:              row.LinkedAt.UTC(),
		})
	}
	return out, nil
}

func (r *AdminRepo) ListAlertDeliveries(ctx context.Context, f admin.DeliveryFilter, limit int, before *admin.ReportCursor) ([]admin.AlertDelivery, error) {
	beforeTs, beforeID := cursorParams(before)
	fromTs, toTs := rangeParams(f.From, f.To)
	rows, err := r.q.ListAlertDeliveriesAdmin(ctx, ListAlertDeliveriesAdminParams{
		Limit:      int32(limit),
		Status:     textParamOpt(f.Status),
		RuleID:     uuidParamOpt(f.RuleID),
		DeviceID:   uuidParamOpt(f.DeviceID),
		ApnsStatus: int4Param(f.APNsStatus),
		FromTs:     fromTs,
		ToTs:       toTs,
		BeforeTs:   beforeTs,
		BeforeID:   beforeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.AlertDelivery, 0, len(rows))
	for _, row := range rows {
		var spotID *uuid.UUID
		if row.SpotID.Valid {
			id := uuid.UUID(row.SpotID.Bytes)
			spotID = &id
		}
		out = append(out, admin.AlertDelivery{
			RuleID:      row.RuleID,
			WindowStart: row.WindowStart.UTC(),
			Status:      row.Status,
			Attempts:    int(row.Attempts),
			ClaimedAt:   row.ClaimedAt.UTC(),
			SentAt:      timePtr(row.SentAt),
			APNsStatus:  intPtr(row.ApnsStatus),
			LastError:   textPtr(row.LastError),
			DeviceID:    row.DeviceID,
			SpotID:      spotID,
		})
	}
	return out, nil
}

func (r *AdminRepo) ListAppleNotifications(ctx context.Context, f admin.AppleNotificationFilter, limit int, before *admin.ReportCursor) ([]admin.AppleNotification, error) {
	beforeTs, beforeID := cursorParams(before)
	fromTs, toTs := rangeParams(f.From, f.To)
	rows, err := r.q.ListAppleNotificationsAdmin(ctx, ListAppleNotificationsAdminParams{
		Limit:       int32(limit),
		Status:      textParamOpt(f.Status),
		Type:        textParamOpt(f.Type),
		Environment: textParamOpt(f.Environment),
		Txn:         textParamOpt(f.Txn),
		FromTs:      fromTs,
		ToTs:        toTs,
		BeforeTs:    beforeTs,
		BeforeID:    beforeID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.AppleNotification, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.AppleNotification{
			ID:                    row.NotificationUuid,
			Type:                  row.Type,
			Subtype:               textPtr(row.Subtype),
			OriginalTransactionID: textPtr(row.OriginalTransactionID),
			SignedDate:            row.SignedDate.UTC(),
			Environment:           row.Environment,
			Status:                row.Status,
			Attempts:              int(row.Attempts),
			LastError:             textPtr(row.LastError),
			ReceivedAt:            row.ReceivedAt.UTC(),
			ProcessedAt:           timePtr(row.ProcessedAt),
		})
	}
	return out, nil
}

func (r *AdminRepo) ListProviderQuotas(ctx context.Context, since time.Time) ([]admin.ProviderQuota, error) {
	rows, err := r.q.ListProviderQuotas(ctx, pgtype.Date{Time: since, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]admin.ProviderQuota, 0, len(rows))
	for _, row := range rows {
		var day time.Time
		if row.Day.Valid {
			day = row.Day.Time.UTC()
		}
		out = append(out, admin.ProviderQuota{
			Provider:  row.Provider,
			Day:       day,
			Used:      int(row.Used),
			UpdatedAt: row.UpdatedAt.UTC(),
		})
	}
	return out, nil
}

func (r *AdminRepo) ListJobRunsForJob(ctx context.Context, job string, limit int) ([]admin.JobRun, error) {
	rows, err := r.q.ListRecentJobRuns(ctx, ListRecentJobRunsParams{Job: job, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]admin.JobRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, jobRunFromModel(row))
	}
	return out, nil
}

func (r *AdminRepo) ListIngestFeatures(ctx context.Context, f admin.IngestFeatureFilter, limit int, before *admin.IngestFeatureCursor) ([]admin.IngestFeature, error) {
	params := ListIngestFeaturesAdminParams{
		Limit:    int32(limit),
		PackSlug: textParamOpt(f.PackSlug),
		Role:     textParamOpt(f.Role),
		Source:   textParamOpt(f.Source),
		RunID:    bigintParam(f.RunID),
	}
	if before != nil {
		params.BeforeTs = pgtype.Timestamptz{Time: before.SeenAt, Valid: true}
		params.BeforeSource = pgtype.Text{String: before.Source, Valid: true}
		params.BeforeSourceID = pgtype.Text{String: before.SourceID, Valid: true}
	}
	rows, err := r.q.ListIngestFeaturesAdmin(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]admin.IngestFeature, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.IngestFeature{
			PackID:        row.PackID,
			PackSlug:      row.PackSlug,
			Source:        row.Source,
			SourceID:      row.SourceID,
			Role:          row.Role,
			Kind:          textPtr(row.Kind),
			Amenity:       textPtr(row.Amenity),
			Quality:       int2Ptr(row.Quality),
			ExcludeReason: textPtr(row.ExcludeReason),
			Version:       intPtr(row.Version),
			RunID:         int64Ptr(row.RunID),
			SeenAt:        row.SeenAt.UTC(),
		})
	}
	return out, nil
}

func int64Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func (r *AdminRepo) CityPackBySlug(ctx context.Context, slug string) (uuid.UUID, error) {
	row, err := r.q.GetCityPackBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, admin.ErrCityPackNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

func (r *AdminRepo) SetSpotCurated(ctx context.Context, id uuid.UUID, curated bool) (bool, error) {
	n, err := r.q.AdminSetSpotCurated(ctx, AdminSetSpotCuratedParams{ID: id, Curated: curated})
	if err != nil {
		return false, mapTransitionError(err)
	}
	return n > 0, nil
}

func (r *AdminRepo) UpdateSpotName(ctx context.Context, id uuid.UUID, name *string) (bool, error) {
	// Empty string clears the display name (t_spots.name is nullable).
	var n pgtype.Text
	if name != nil && *name != "" {
		n = pgtype.Text{String: *name, Valid: true}
	}
	rows, err := r.q.AdminUpdateSpotName(ctx, AdminUpdateSpotNameParams{ID: id, Name: n})
	if err != nil {
		return false, mapTransitionError(err)
	}
	return rows > 0, nil
}

func (r *AdminRepo) SpotPackAndStatus(ctx context.Context, id uuid.UUID) (uuid.UUID, string, *uuid.UUID, error) {
	row, err := r.q.GetSpotPackAndStatus(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", nil, admin.ErrSpotNotFound
	}
	if err != nil {
		return uuid.Nil, "", nil, err
	}
	return row.CityPackID, row.Status, uuidPtr(row.MergedInto), nil
}

func (r *AdminRepo) MergeSpot(ctx context.Context, id, target uuid.UUID) (bool, error) {
	n, err := r.q.AdminMergeSpot(ctx, AdminMergeSpotParams{
		ID:         id,
		MergedInto: pgtype.UUID{Bytes: target, Valid: true},
	})
	if err != nil {
		return false, mapTransitionError(err)
	}
	return n > 0, nil
}

func (r *AdminRepo) UnmergeSpot(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.AdminUnmergeSpot(ctx, id)
	if err != nil {
		return false, mapTransitionError(err)
	}
	return n > 0, nil
}
