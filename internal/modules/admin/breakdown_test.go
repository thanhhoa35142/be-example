package admin

import (
	"context"
	"errors"
	"testing"
	"time"
)

type breakdownRepo struct {
	Repository
	got EventBreakdownInput
}

func (r *breakdownRepo) EventBreakdown(_ context.Context, in EventBreakdownInput) (EventBreakdown, error) {
	r.got = in
	return EventBreakdown{Name: in.Name}, nil
}

func TestEventBreakdownValidation(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	bad := []EventBreakdownInput{
		{Name: "", Prop: "choice"},
		{Name: "Paywall", Prop: "choice"},
		{Name: "paywall_feedback", Prop: ""},
		{Name: "paywall_feedback", Prop: "1choice"},
		{Name: "paywall_feedback", Prop: "choice'--"},
		{Name: "paywall_feedback", Prop: "a.b"},
		{Name: "paywall_feedback", Prop: "choice) OR 1=1"},
		{Name: "paywall_feedback", Prop: "aaaaaaaaaabbbbbbbbbbccccccccccddd"}, // 33 chars
		{Name: "paywall_feedback", Prop: "choice", By: "re ason"},
		{Name: "paywall_feedback", Prop: "choice", By: "_x"},
	}
	for _, in := range bad {
		repo := &breakdownRepo{}
		_, err := NewService(repo).EventBreakdown(context.Background(), in, now)
		if !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("%+v: err=%v, want ErrInvalidFilter", in, err)
		}
	}
	repo := &breakdownRepo{}
	if _, err := NewService(repo).EventBreakdown(context.Background(),
		EventBreakdownInput{Name: "paywall_feedback", Prop: "choice", By: "reason"}, now); err != nil {
		t.Fatal(err)
	}
	if !repo.got.Since.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("since default: %v", repo.got.Since)
	}
	// 32 chars is the max.
	if _, err := NewService(repo).EventBreakdown(context.Background(),
		EventBreakdownInput{Name: "e", Prop: "aaaaaaaaaabbbbbbbbbbccccccccccdd"}, now); err != nil {
		t.Fatalf("32-char prop: %v", err)
	}
}
