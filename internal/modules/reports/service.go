package reports

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

func (s *Service) SubmitSpotReport(ctx context.Context, in SpotReportInput) error {
	if !validSpotReportType(in.Type) {
		return ErrInvalidReport
	}
	if in.Type == ReportIssue {

		if in.IssueCode == nil || !validIssueCode(*in.IssueCode) {
			return ErrInvalidReport
		}
	} else {
		in.IssueCode = nil
	}
	active, err := s.repo.DeviceActive(ctx, in.DeviceID)
	if err != nil {
		return err
	}
	if !active {
		return ErrDeviceNotFound
	}
	exists, err := s.repo.SpotActive(ctx, in.SpotID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSpotNotFound
	}
	n, err := s.repo.SpotReportsSince(ctx, in.DeviceID, s.now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if n >= SpotReportRateLimit {
		return ErrRateLimited
	}
	err = s.repo.InsertSpotReport(ctx, in)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicateReport
	}
	return err
}

func (s *Service) SubmitCrowdReport(ctx context.Context, spotID, deviceID uuid.UUID, level int) error {
	if level < CrowdQuiet || level > CrowdBusy {
		return ErrInvalidReport
	}
	active, err := s.repo.DeviceActive(ctx, deviceID)
	if err != nil {
		return err
	}
	if !active {
		return ErrDeviceNotFound
	}
	exists, err := s.repo.SpotActive(ctx, spotID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSpotNotFound
	}

	n, err := s.repo.CrowdReportsSince(ctx, deviceID, s.now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if n >= CrowdRateLimit {
		return ErrRateLimited
	}

	tz, err := s.repo.DeviceTZ(ctx, deviceID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	local := s.now().In(loc)
	return s.repo.InsertCrowdReport(ctx, spotID, deviceID, level,
		int(local.Weekday()), local.Hour())
}

func (s *Service) SubmitScoreFeedback(ctx context.Context, in ScoreFeedbackInput) error {
	if in.Verdict < -1 || in.Verdict > 1 {
		return ErrInvalidReport
	}

	now := s.now()
	if !in.TSHour.Equal(in.TSHour.Truncate(time.Hour)) ||
		in.TSHour.Before(now.Add(-FeedbackMaxAge)) || in.TSHour.After(now.Add(FeedbackMaxAhead)) {
		return ErrInvalidReport
	}
	active, err := s.repo.DeviceActive(ctx, in.DeviceID)
	if err != nil {
		return err
	}
	if !active {
		return ErrDeviceNotFound
	}
	exists, err := s.repo.SpotActive(ctx, in.SpotID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSpotNotFound
	}
	n, err := s.repo.ScoreFeedbackSince(ctx, in.DeviceID, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if n >= FeedbackRateLimit {
		return ErrRateLimited
	}
	return s.repo.InsertScoreFeedback(ctx, in)
}

func validIssueCode(c string) bool {
	if c == "" || len(c) > MaxIssueCodeLen {
		return false
	}
	for _, r := range c {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

func validSpotReportType(t string) bool {
	switch t {
	case ReportConfirm, ReportMissing, ReportClosed, ReportIssue:
		return true
	}
	return false
}
