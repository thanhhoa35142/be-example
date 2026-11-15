package osm

import "testing"

func TestNextStatus(t *testing.T) {
	p := DefaultPolicy
	cases := []struct {
		name string
		ev   Evidence
		want Status
		reas string
	}{
		{"hidden terminal", Evidence{CurrentStatus: StatusHidden, Quality: 100, Trusted: true}, StatusHidden, ""},
		{"merged terminal", Evidence{CurrentStatus: StatusMerged, SourceChanged: true}, StatusMerged, ""},
		{"pending auto-activate", Evidence{CurrentStatus: StatusPending, Quality: 80, Trusted: true}, StatusActive, "auto_quality"},
		{"pending low quality untrusted", Evidence{CurrentStatus: StatusPending, Quality: 80, Trusted: false}, StatusPending, "awaiting_review"},
		{"pending vanished", Evidence{CurrentStatus: StatusPending, AllSourcesMissing: true, Quality: 90, Trusted: true}, StatusHidden, "vanished_before_review"},
		{"active closes after missing runs", Evidence{CurrentStatus: StatusActive, AllSourcesMissing: true, MissingRuns: 2, Quality: 80}, StatusClosed, "source_missing"},
		{"active survives confirm shield", Evidence{CurrentStatus: StatusActive, AllSourcesMissing: true, MissingRuns: 5, RecentConfirm: true}, StatusActive, ""},
		{"active missing but runs < threshold", Evidence{CurrentStatus: StatusActive, AllSourcesMissing: true, MissingRuns: 1}, StatusActive, ""},
		{"closed by report stays", Evidence{CurrentStatus: StatusClosed, ClosedByReport: true, SourceChanged: true}, StatusClosed, "closed_by_report"},
		{"closed by import reopens", Evidence{CurrentStatus: StatusClosed, SourceChanged: true}, StatusActive, "source_returned"},
		{"admin-hidden stays hidden", Evidence{CurrentStatus: StatusHidden, StatusReason: "moderation", SourcePresent: true, Quality: 100, Trusted: true}, StatusHidden, ""},
		{"importer-hidden re-activates", Evidence{CurrentStatus: StatusHidden, StatusReason: ReasonBelowFloor, SourcePresent: true, Quality: 70, Trusted: true}, StatusActive, ReasonAutoQuality},
		{"importer-hidden back to pending", Evidence{CurrentStatus: StatusHidden, StatusReason: ReasonVanished, SourcePresent: true, Quality: 40, Trusted: true}, StatusPending, ReasonAwaitingReview},
		{"importer-hidden still below floor", Evidence{CurrentStatus: StatusHidden, StatusReason: ReasonBelowFloor, SourcePresent: true, Quality: 20, Trusted: true}, StatusHidden, ReasonBelowFloor},
		{"source_missing reopens unchanged", Evidence{CurrentStatus: StatusClosed, StatusReason: ReasonSourceMissing, SourcePresent: true}, StatusActive, ReasonSourceReturned},
		{"admin-closed stays closed", Evidence{CurrentStatus: StatusClosed, StatusReason: "permanently_closed", SourcePresent: true}, StatusClosed, ""},
		{"curated untouched", Evidence{CurrentStatus: StatusActive, Curated: true, AllSourcesMissing: true, MissingRuns: 9}, StatusActive, "curated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := p.NextStatus(tc.ev)
			if got != tc.want || reason != tc.reas {
				t.Fatalf("NextStatus = %s/%q, want %s/%q", got, reason, tc.want, tc.reas)
			}
		})
	}
}

func TestNewSpotStatus(t *testing.T) {
	p := DefaultPolicy
	if s, _ := p.NewSpotStatus(Evidence{Quality: 90, Trusted: true}); s != StatusActive {
		t.Fatalf("high quality trusted → %s, want active", s)
	}
	if s, _ := p.NewSpotStatus(Evidence{Quality: 50, Trusted: true}); s != StatusPending {
		t.Fatalf("mid quality → %s, want pending", s)
	}
	if s, r := p.NewSpotStatus(Evidence{Quality: 10}); s != "" || r != "below_quality_floor" {
		t.Fatalf("low quality → %q/%q, want raw-only", s, r)
	}
}

func TestNormalizeOsmiumID(t *testing.T) {
	for in, want := range map[string]string{
		"a246": "w123", "a247": "r123", "a0": "w0",
		"n5": "n5", "w7": "w7", "r9": "r9", "a": "a", "axyz": "axyz",
	} {
		if got := NormalizeOsmiumID(in); got != want {
			t.Errorf("NormalizeOsmiumID(%q) = %q, want %q", in, got, want)
		}
	}
}
