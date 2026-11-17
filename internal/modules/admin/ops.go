package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
)

var (
	ErrInvalidOpsFilter = errors.New("invalid ops filter")
	ErrDeviceNotFound   = errors.New("device not found")
	ErrInvalidSpotPatch = errors.New("invalid spot patch")
	ErrInvalidPackSlug  = errors.New("invalid pack slug")
)

// paginate trims a limit+1 repo page into a Page with an encoded cursor.
func paginate[T any](rows []T, limit int, encode func(T) string) Page[T] {
	if len(rows) > limit {
		cur := encode(rows[limit-1])
		return Page[T]{Items: rows[:limit], NextCursor: &cur}
	}
	return Page[T]{Items: rows}
}

// reportPage is the shared keyset pager for (created_at, id) DESC lists that
// reuse ReportCursor.
func reportPage[T interface {
	GetCreatedAt() time.Time
	GetID() uuid.UUID
}](rows []T, limit int) Page[T] {
	return paginate(rows, limit, func(r T) string {
		return ReportCursor{CreatedAt: r.GetCreatedAt(), ID: r.GetID()}.Encode()
	})
}

// ---- moderation history ----

type SpotReport struct {
	ID         uuid.UUID  `json:"id"`
	SpotID     uuid.UUID  `json:"spotId"`
	SpotName   string     `json:"spotName"`
	DeviceID   uuid.UUID  `json:"deviceId"`
	Type       string     `json:"type"`
	IssueCode  *string    `json:"issueCode"`
	Status     string     `json:"status"`
	ReviewedAt *time.Time `json:"reviewedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

func (r SpotReport) GetCreatedAt() time.Time { return r.CreatedAt }
func (r SpotReport) GetID() uuid.UUID        { return r.ID }

type SpotReportFilter struct {
	Status string
	Type   string
	SpotID *uuid.UUID
	From   time.Time
	To     time.Time
}

var spotReportStatuses = map[string]bool{
	"pending": true, "accepted": true, "rejected": true, "duplicate": true,
}

var spotReportTypes = map[string]bool{
	"confirm": true, "missing": true, "closed": true, "issue": true,
}

func (s *Service) SpotReports(ctx context.Context, f SpotReportFilter, limit int, before *ReportCursor) (Page[SpotReport], error) {
	if f.Status != "" && !spotReportStatuses[f.Status] {
		return Page[SpotReport]{}, fmt.Errorf("%w: unknown status %q", ErrInvalidOpsFilter, f.Status)
	}
	if f.Type != "" && !spotReportTypes[f.Type] {
		return Page[SpotReport]{}, fmt.Errorf("%w: unknown type %q", ErrInvalidOpsFilter, f.Type)
	}
	from, to, err := normalizeRange(f.From, f.To)
	if err != nil {
		return Page[SpotReport]{}, err
	}
	f.From, f.To = from, to
	rows, err := s.repo.ListSpotReports(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[SpotReport]{}, err
	}
	return reportPage(rows, clampLimit(limit)), nil
}

func (s *Service) SpotReportDetail(ctx context.Context, id uuid.UUID) (*SpotReport, error) {
	return s.repo.GetSpotReport(ctx, id)
}

// ---- crowd reports / score feedbacks ----

type CrowdReport struct {
	ID        uuid.UUID `json:"id"`
	SpotID    uuid.UUID `json:"spotId"`
	SpotName  string    `json:"spotName"`
	DeviceID  uuid.UUID `json:"deviceId"`
	Level     int       `json:"level"`
	LocalDow  int       `json:"localDow"`
	LocalHour int       `json:"localHour"`
	CreatedAt time.Time `json:"createdAt"`
}

func (r CrowdReport) GetCreatedAt() time.Time { return r.CreatedAt }
func (r CrowdReport) GetID() uuid.UUID        { return r.ID }

type CrowdReportFilter struct {
	SpotID   *uuid.UUID
	DeviceID *uuid.UUID
	Level    *int
	From     time.Time
	To       time.Time
}

func (s *Service) CrowdReports(ctx context.Context, f CrowdReportFilter, limit int, before *ReportCursor) (Page[CrowdReport], error) {
	if f.Level != nil && (*f.Level < 1 || *f.Level > 3) {
		return Page[CrowdReport]{}, fmt.Errorf("%w: level must be 1-3", ErrInvalidOpsFilter)
	}
	from, to, err := normalizeRange(f.From, f.To)
	if err != nil {
		return Page[CrowdReport]{}, err
	}
	f.From, f.To = from, to
	rows, err := s.repo.ListCrowdReports(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[CrowdReport]{}, err
	}
	return reportPage(rows, clampLimit(limit)), nil
}

type ScoreFeedback struct {
	ID        uuid.UUID `json:"id"`
	SpotID    uuid.UUID `json:"spotId"`
	SpotName  string    `json:"spotName"`
	TSHour    time.Time `json:"tsHour"`
	DeviceID  uuid.UUID `json:"deviceId"`
	Verdict   int       `json:"verdict"`
	CreatedAt time.Time `json:"createdAt"`
}

func (f ScoreFeedback) GetCreatedAt() time.Time { return f.CreatedAt }
func (f ScoreFeedback) GetID() uuid.UUID        { return f.ID }

type ScoreFeedbackFilter struct {
	SpotID   *uuid.UUID
	DeviceID *uuid.UUID
	Verdict  *int
	From     time.Time
	To       time.Time
}

func (s *Service) ScoreFeedbacks(ctx context.Context, f ScoreFeedbackFilter, limit int, before *ReportCursor) (Page[ScoreFeedback], error) {
	if f.Verdict != nil && (*f.Verdict < -1 || *f.Verdict > 1) {
		return Page[ScoreFeedback]{}, fmt.Errorf("%w: verdict must be -1, 0 or 1", ErrInvalidOpsFilter)
	}
	from, to, err := normalizeRange(f.From, f.To)
	if err != nil {
		return Page[ScoreFeedback]{}, err
	}
	f.From, f.To = from, to
	rows, err := s.repo.ListScoreFeedbacks(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[ScoreFeedback]{}, err
	}
	return reportPage(rows, clampLimit(limit)), nil
}

func normalizeRange(from, to time.Time) (time.Time, time.Time, error) {
	if from.IsZero() && to.IsZero() {
		return from, to, nil
	}
	f, t := from.UTC(), to.UTC()
	if f.IsZero() {
		f = t.Add(-366 * 24 * time.Hour)
	}
	if t.IsZero() {
		t = time.Now().UTC()
	}
	if !f.Before(t) || t.Sub(f) > 366*24*time.Hour {
		return from, to, ErrInvalidOpsFilter
	}
	return f, t, nil
}

// ---- devices ----

type AdminDevice struct {
	ID         uuid.UUID  `json:"id"`
	APNsEnv    *string    `json:"apnsEnv"`
	PushStatus string     `json:"pushStatus"`
	Locale     *string    `json:"locale"`
	TZ         *string    `json:"tz"`
	AppVersion *string    `json:"appVersion"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
	DeletedAt  *time.Time `json:"deletedAt"`
	LinkedSubs int64      `json:"linkedSubscriptions"`
}

func (d AdminDevice) GetCreatedAt() time.Time { return d.CreatedAt }
func (d AdminDevice) GetID() uuid.UUID        { return d.ID }

type AdminDeviceFilter struct {
	ID             *uuid.UUID
	PushStatus     string
	IncludeDeleted bool
}

var devicePushStatuses = map[string]bool{"none": true, "active": true, "unregistered": true}

func (s *Service) Devices(ctx context.Context, f AdminDeviceFilter, limit int, before *ReportCursor) (Page[AdminDevice], error) {
	if f.PushStatus != "" && !devicePushStatuses[f.PushStatus] {
		return Page[AdminDevice]{}, fmt.Errorf("%w: unknown push status %q", ErrInvalidOpsFilter, f.PushStatus)
	}
	rows, err := s.repo.ListDevices(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[AdminDevice]{}, err
	}
	return reportPage(rows, clampLimit(limit)), nil
}

type DeviceSubscription struct {
	OriginalTransactionID string     `json:"originalTransactionId"`
	ProductID             string     `json:"productId"`
	Status                string     `json:"status"`
	Environment           string     `json:"environment"`
	Ownership             string     `json:"ownership"`
	IsTrial               bool       `json:"isTrial"`
	AutoRenew             bool       `json:"autoRenew"`
	ExpiresAt             *time.Time `json:"expiresAt"`
	GraceExpiresAt        *time.Time `json:"graceExpiresAt"`
	RevokedAt             *time.Time `json:"revokedAt"`
	LinkedAt              time.Time  `json:"linkedAt"`
}

type AdminDeviceDetail struct {
	AdminDevice
	Prefs         map[string]any       `json:"prefs"`
	Subscriptions []DeviceSubscription `json:"subscriptions"`
	Plan          string               `json:"plan"`
}

// payingStatuses mirror the entitlement plan check: a device with any
// subscription in one of these states is on the plus plan.
var payingStatuses = map[string]bool{"active": true, "grace": true, "billing_retry": true}

func (s *Service) DeviceDetail(ctx context.Context, id uuid.UUID) (*AdminDeviceDetail, error) {
	d, err := s.repo.GetDevice(ctx, id)
	if err != nil {
		return nil, err
	}
	subs, err := s.repo.ListDeviceSubscriptions(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Subscriptions = subs
	d.Plan = string(entitlement.PlanFree)
	for _, sub := range subs {
		if payingStatuses[sub.Status] {
			d.Plan = string(entitlement.PlanPlus)
			break
		}
	}
	return d, nil
}

// ---- notification deliveries + apple notifications ----

type AlertDelivery struct {
	RuleID      uuid.UUID  `json:"ruleId"`
	WindowStart time.Time  `json:"windowStart"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	ClaimedAt   time.Time  `json:"claimedAt"`
	SentAt      *time.Time `json:"sentAt"`
	APNsStatus  *int       `json:"apnsStatus"`
	LastError   *string    `json:"lastError"`
	DeviceID    uuid.UUID  `json:"deviceId"`
	SpotID      *uuid.UUID `json:"spotId"`
}

func (d AlertDelivery) GetCreatedAt() time.Time { return d.WindowStart }
func (d AlertDelivery) GetID() uuid.UUID        { return d.RuleID }

type DeliveryFilter struct {
	Status     string
	RuleID     *uuid.UUID
	DeviceID   *uuid.UUID
	APNsStatus *int
	From       time.Time
	To         time.Time
}

var deliveryStatuses = map[string]bool{
	"claimed": true, "sent": true, "failed": true, "dead": true,
}

func (s *Service) AlertDeliveries(ctx context.Context, f DeliveryFilter, limit int, before *ReportCursor) (Page[AlertDelivery], error) {
	if f.Status != "" && !deliveryStatuses[f.Status] {
		return Page[AlertDelivery]{}, fmt.Errorf("%w: unknown status %q", ErrInvalidOpsFilter, f.Status)
	}
	from, to, err := normalizeRange(f.From, f.To)
	if err != nil {
		return Page[AlertDelivery]{}, err
	}
	f.From, f.To = from, to
	rows, err := s.repo.ListAlertDeliveries(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[AlertDelivery]{}, err
	}
	return reportPage(rows, clampLimit(limit)), nil
}

type AppleNotification struct {
	ID                    uuid.UUID  `json:"id"`
	Type                  string     `json:"type"`
	Subtype               *string    `json:"subtype"`
	OriginalTransactionID *string    `json:"originalTransactionId"`
	SignedDate            time.Time  `json:"signedDate"`
	Environment           string     `json:"environment"`
	Status                string     `json:"status"`
	Attempts              int        `json:"attempts"`
	LastError             *string    `json:"lastError"`
	ReceivedAt            time.Time  `json:"receivedAt"`
	ProcessedAt           *time.Time `json:"processedAt"`
}

func (n AppleNotification) GetCreatedAt() time.Time { return n.ReceivedAt }
func (n AppleNotification) GetID() uuid.UUID        { return n.ID }

type AppleNotificationFilter struct {
	Status      string
	Type        string
	Environment string
	Txn         string
	From        time.Time
	To          time.Time
}

var appleNotificationStatuses = map[string]bool{
	"received": true, "processed": true, "ignored": true, "failed": true, "dead": true,
}

var appleEnvironments = map[string]bool{"production": true, "sandbox": true}

func (s *Service) AppleNotifications(ctx context.Context, f AppleNotificationFilter, limit int, before *ReportCursor) (Page[AppleNotification], error) {
	if f.Status != "" && !appleNotificationStatuses[f.Status] {
		return Page[AppleNotification]{}, fmt.Errorf("%w: unknown status %q", ErrInvalidOpsFilter, f.Status)
	}
	if f.Environment != "" && !appleEnvironments[f.Environment] {
		return Page[AppleNotification]{}, fmt.Errorf("%w: unknown environment %q", ErrInvalidOpsFilter, f.Environment)
	}
	from, to, err := normalizeRange(f.From, f.To)
	if err != nil {
		return Page[AppleNotification]{}, err
	}
	f.From, f.To = from, to
	rows, err := s.repo.ListAppleNotifications(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[AppleNotification]{}, err
	}
	return reportPage(rows, clampLimit(limit)), nil
}

// ---- provider health ----

type ProviderQuota struct {
	Provider  string    `json:"provider"`
	Day       time.Time `json:"day"`
	Used      int       `json:"used"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ProviderHealth struct {
	Quotas      []ProviderQuota `json:"quotas"`
	WeatherRuns []JobRun        `json:"weatherRuns"`
}

const (
	providerQuotaDays   = 14
	providerHealthLimit = 20
)

func (s *Service) ProviderHealth(ctx context.Context) (ProviderHealth, error) {
	h := ProviderHealth{Quotas: []ProviderQuota{}, WeatherRuns: []JobRun{}}
	since := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(providerQuotaDays - 1))
	quotas, err := s.repo.ListProviderQuotas(ctx, since)
	if err != nil {
		return h, err
	}
	h.Quotas = quotas
	runs, err := s.repo.ListJobRunsForJob(ctx, "weather.refresh", providerHealthLimit)
	if err != nil {
		return h, err
	}
	h.WeatherRuns = runs
	return h, nil
}

// ---- ingest features + per-pack run ----

type IngestFeature struct {
	PackID        uuid.UUID `json:"packId"`
	PackSlug      string    `json:"packSlug"`
	Source        string    `json:"source"`
	SourceID      string    `json:"sourceId"`
	Role          string    `json:"role"`
	Kind          *string   `json:"kind"`
	Amenity       *string   `json:"amenity"`
	Quality       *int      `json:"quality"`
	ExcludeReason *string   `json:"excludeReason"`
	Version       *int      `json:"version"`
	RunID         *int64    `json:"runId"`
	SeenAt        time.Time `json:"seenAt"`
}

// IngestFeatureCursor keysets on (seen_at, source, source_id) DESC.
type IngestFeatureCursor struct {
	SeenAt   time.Time
	Source   string
	SourceID string
}

func (c IngestFeatureCursor) Encode() string {
	return fmt.Sprintf("%d|%s|%s", c.SeenAt.UnixNano(), c.Source, c.SourceID)
}

func ParseIngestFeatureCursor(s string) (IngestFeatureCursor, error) {
	nanos, rest, ok := strings.Cut(s, "|")
	if !ok {
		return IngestFeatureCursor{}, ErrInvalidCursor
	}
	source, sourceID, ok := strings.Cut(rest, "|")
	if !ok || source == "" || sourceID == "" {
		return IngestFeatureCursor{}, ErrInvalidCursor
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return IngestFeatureCursor{}, ErrInvalidCursor
	}
	return IngestFeatureCursor{SeenAt: time.Unix(0, n).UTC(), Source: source, SourceID: sourceID}, nil
}

type IngestFeatureFilter struct {
	PackSlug string
	Role     string
	Source   string
	RunID    *int64
}

var ingestFeatureRoles = map[string]bool{"candidate": true, "amenity": true, "excluded": true}

func (s *Service) IngestFeatures(ctx context.Context, f IngestFeatureFilter, limit int, before *IngestFeatureCursor) (Page[IngestFeature], error) {
	if f.Role != "" && !ingestFeatureRoles[f.Role] {
		return Page[IngestFeature]{}, fmt.Errorf("%w: unknown role %q", ErrInvalidOpsFilter, f.Role)
	}
	rows, err := s.repo.ListIngestFeatures(ctx, f, clampLimit(limit), before)
	if err != nil {
		return Page[IngestFeature]{}, err
	}
	return paginate(rows, clampLimit(limit), func(r IngestFeature) string {
		return IngestFeatureCursor{SeenAt: r.SeenAt, Source: r.Source, SourceID: r.SourceID}.Encode()
	}), nil
}

// RequestPackRun asks the worker to run ingest.osm for a single city pack.
func (s *Service) RequestPackRun(ctx context.Context, slug string) (JobSchedule, error) {
	if !validPackSlug(slug) {
		return JobSchedule{}, ErrInvalidPackSlug
	}
	if _, err := s.repo.CityPackBySlug(ctx, slug); err != nil {
		return JobSchedule{}, err
	}
	return s.repo.RequestJobRunForPack(ctx, "ingest.osm", slug)
}

func validPackSlug(slug string) bool {
	if len(slug) == 0 || len(slug) > 100 {
		return false
	}
	for _, r := range slug {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// ---- spot lifecycle ----

// SpotPatch is the admin edit surface for a single spot. Exactly one of the
// fields may be set per call.
type SpotPatch struct {
	Action   string     // hide|restore|approve|merge|unmerge
	TargetID *uuid.UUID // merge only: surviving spot
	Name     *string    // rename; empty string clears the name
	Curated  *bool      // curation lock
}

func (s *Service) UpdateSpot(ctx context.Context, id uuid.UUID, p SpotPatch) (SpotPatchResult, error) {
	switch {
	case p.Action != "":
		return s.spotAction(ctx, id, p)
	case p.Name != nil:
		changed, err := s.repo.UpdateSpotName(ctx, id, p.Name)
		if err != nil {
			return SpotPatchResult{}, err
		}
		if !changed {
			if _, _, _, err := s.repo.SpotPackAndStatus(ctx, id); err != nil {
				return SpotPatchResult{}, err
			}
		}
		return SpotPatchResult{Changed: changed}, nil
	case p.Curated != nil:
		changed, err := s.repo.SetSpotCurated(ctx, id, *p.Curated)
		if err != nil {
			return SpotPatchResult{}, err
		}
		if !changed {
			if _, _, _, err := s.repo.SpotPackAndStatus(ctx, id); err != nil {
				return SpotPatchResult{}, err
			}
		}
		return SpotPatchResult{Changed: changed}, nil
	default:
		return SpotPatchResult{}, ErrInvalidSpotPatch
	}
}

type SpotPatchResult struct {
	Status  string `json:"status,omitempty"`
	Changed bool   `json:"changed"`
}

func (s *Service) spotAction(ctx context.Context, id uuid.UUID, p SpotPatch) (SpotPatchResult, error) {
	switch p.Action {
	case "merge":
		if p.TargetID == nil {
			return SpotPatchResult{}, fmt.Errorf("%w: merge needs targetId", ErrInvalidSpotPatch)
		}
		changed, err := s.mergeSpot(ctx, id, *p.TargetID)
		if err != nil {
			return SpotPatchResult{}, err
		}
		return SpotPatchResult{Status: "merged", Changed: changed}, nil
	case "unmerge":
		changed, err := s.repo.UnmergeSpot(ctx, id)
		if err != nil {
			return SpotPatchResult{}, err
		}
		if !changed {
			_, status, _, err := s.repo.SpotPackAndStatus(ctx, id)
			if err != nil {
				return SpotPatchResult{}, err
			}
			if status != "merged" {
				return SpotPatchResult{}, fmt.Errorf("%w: cannot unmerge a %s spot", ErrInvalidTransition, status)
			}
		}
		return SpotPatchResult{Status: "pending", Changed: changed}, nil
	}

	status, changed, err := s.SetSpotVisibility(ctx, id, p.Action)
	if err != nil {
		return SpotPatchResult{}, err
	}
	return SpotPatchResult{Status: status, Changed: changed}, nil
}

func (s *Service) mergeSpot(ctx context.Context, id, target uuid.UUID) (bool, error) {
	if id == target {
		return false, fmt.Errorf("%w: cannot merge a spot into itself", ErrInvalidSpotPatch)
	}
	srcPack, srcStatus, srcMerged, err := s.repo.SpotPackAndStatus(ctx, id)
	if err != nil {
		return false, err
	}
	dstPack, dstStatus, dstMerged, err := s.repo.SpotPackAndStatus(ctx, target)
	if err != nil {
		return false, err
	}
	if srcStatus == "merged" || srcMerged != nil {
		return false, fmt.Errorf("%w: spot is already merged", ErrInvalidTransition)
	}
	if dstStatus == "merged" || dstMerged != nil {
		return false, fmt.Errorf("%w: merge target is itself merged", ErrInvalidTransition)
	}
	if dstStatus != "active" && dstStatus != "pending" {
		return false, fmt.Errorf("%w: merge target must be active or pending", ErrInvalidTransition)
	}
	if srcPack != dstPack {
		return false, fmt.Errorf("%w: spots must belong to the same city pack", ErrInvalidSpotPatch)
	}
	changed, err := s.repo.MergeSpot(ctx, id, target)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, fmt.Errorf("%w: spot changed while merging", ErrInvalidTransition)
	}
	return true, nil
}
