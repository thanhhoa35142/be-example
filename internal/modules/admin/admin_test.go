package admin

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReportCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	ts := time.Date(2025, 6, 1, 12, 0, 0, 123456789, time.UTC)
	cur := ReportCursor{CreatedAt: ts, ID: id}

	got, err := ParseReportCursor(cur.Encode())
	if err != nil {
		t.Fatalf("ParseReportCursor: %v", err)
	}
	if !got.CreatedAt.Equal(ts) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, ts)
	}
	if got.ID != id {
		t.Fatalf("ID = %v, want %v", got.ID, id)
	}
}

func TestParseReportCursorInvalid(t *testing.T) {
	for _, s := range []string{"", "no-separator", "abc|" + uuid.NewString(), "123|not-a-uuid", "|"} {
		if _, err := ParseReportCursor(s); err == nil {
			t.Fatalf("ParseReportCursor(%q) expected error", s)
		}
	}
}
