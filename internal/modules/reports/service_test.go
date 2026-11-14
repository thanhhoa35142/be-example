package reports

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memRepo struct {
	Repository
	spotOK             bool
	spotReports        int
	feedback           int
	inserted, fbStored int
}

func (r *memRepo) DeviceActive(context.Context, uuid.UUID) (bool, error) { return true, nil }
func (r *memRepo) SpotActive(context.Context, uuid.UUID) (bool, error)   { return r.spotOK, nil }
func (r *memRepo) SpotReportsSince(context.Context, uuid.UUID, time.Time) (int, error) {
	return r.spotReports, nil
}
func (r *memRepo) ScoreFeedbackSince(context.Context, uuid.UUID, time.Time) (int, error) {
	return r.feedback, nil
}
func (r *memRepo) InsertSpotReport(context.Context, SpotReportInput) error { r.inserted++; return nil }
func (r *memRepo) InsertScoreFeedback(context.Context, ScoreFeedbackInput) error {
	r.fbStored++
	return nil
}

func newSvc(repo *memRepo) (*Service, time.Time) {
	now := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	s := NewService(repo)
	s.now = func() time.Time { return now }
	return s, now
}

func ptr(s string) *string { return &s }

func TestSpotReportValidationAndRateLimit(t *testing.T) {
	repo := &memRepo{spotOK: true}
	s, _ := newSvc(repo)
	in := func(typ string, code *string) SpotReportInput {
		return SpotReportInput{SpotID: uuid.New(), DeviceID: uuid.New(), Type: typ, IssueCode: code}
	}
	for name, c := range map[string]SpotReportInput{
		"issue without code": in(ReportIssue, nil),
		"issue empty code":   in(ReportIssue, ptr("")),
		"issue prose":        in(ReportIssue, ptr("no toilet here!")),
		"issue too long":     in(ReportIssue, ptr(string(make([]byte, MaxIssueCodeLen+1)))),
		"unknown type":       in("whatever", nil),
	} {
		if err := s.SubmitSpotReport(t.Context(), c); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if repo.inserted != 0 {
		t.Fatal("invalid report inserted")
	}
	if err := s.SubmitSpotReport(t.Context(), in(ReportIssue, ptr("no_toilet"))); err != nil {
		t.Fatalf("valid issue: %v", err)
	}
	// A code on a non-issue report is dropped, not an error (CHECK pairs them).
	if err := s.SubmitSpotReport(t.Context(), in(ReportConfirm, ptr("x"))); err != nil {
		t.Fatalf("confirm with stray code: %v", err)
	}
	repo.spotReports = SpotReportRateLimit
	if err := s.SubmitSpotReport(t.Context(), in(ReportConfirm, nil)); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestScoreFeedbackValidation(t *testing.T) {
	repo := &memRepo{spotOK: true}
	s, now := newSvc(repo)
	hour := now.Truncate(time.Hour)
	fb := func(ts time.Time) ScoreFeedbackInput {
		return ScoreFeedbackInput{SpotID: uuid.New(), DeviceID: uuid.New(), TSHour: ts, Verdict: 1}
	}
	for name, ts := range map[string]time.Time{
		"not on the hour": now,
		"too old":         hour.Add(-FeedbackMaxAge - time.Hour),
		"too far ahead":   hour.Add(FeedbackMaxAhead + time.Hour),
		"zero time":       {},
	} {
		if err := s.SubmitScoreFeedback(t.Context(), fb(ts)); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, ts := range map[string]time.Time{
		"current hour": hour,
		"past edge":    hour.Add(-FeedbackMaxAge + time.Hour),
		"forecast end": hour.Add(7 * 24 * time.Hour),
	} {
		if err := s.SubmitScoreFeedback(t.Context(), fb(ts)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	repo.spotOK = false
	if err := s.SubmitScoreFeedback(t.Context(), fb(hour)); !errors.Is(err, ErrSpotNotFound) {
		t.Errorf("unknown spot: %v", err)
	}
	repo.spotOK, repo.feedback = true, FeedbackRateLimit
	if err := s.SubmitScoreFeedback(t.Context(), fb(hour)); !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limit: %v", err)
	}
}
