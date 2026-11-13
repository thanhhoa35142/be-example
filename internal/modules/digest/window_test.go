package digest

import (
	"testing"
	"time"
)

func hs(start time.Time, scores ...int) []HourScore {
	out := make([]HourScore, len(scores))
	for i, s := range scores {
		out[i] = HourScore{Start: start.Add(time.Duration(i) * time.Hour), Score: s}
	}
	return out
}

var t0 = time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)

func TestBestWindow(t *testing.T) {
	cases := []struct {
		name   string
		in     []HourScore
		ok     bool
		startH int // hour offset from t0
		lenH   int
		mean   float64
	}{
		{"empty", nil, false, 0, 0, 0},
		{"single qualifying hour is too short", hs(t0, 90), false, 0, 0, 0},
		{"all below threshold", hs(t0, 59, 40, 30, 59), false, 0, 0, 0},
		{"exactly 60 qualifies", hs(t0, 60, 60), true, 0, 2, 60},
		{"low hour splits run", hs(t0, 80, 59, 80), false, 0, 0, 0},
		{"mean picks peak pair over long mediocre run", hs(t0, 61, 62, 61, 95, 95), true, 3, 2, 95},
		{"ties prefer longer", hs(t0, 70, 70, 70), true, 0, 3, 70},
		{"ties prefer earlier", hs(t0, 70, 70, 10, 70, 70), true, 0, 2, 70},
		{"cap: flat 8h run advertises 4h", hs(t0, 70, 70, 70, 70, 70, 70, 70, 70), true, 0, 4, 70},
		{"extending a window lowers mean only when worse", hs(t0, 90, 90, 60), true, 0, 2, 90},
		{"gap in hours breaks contiguity", append(hs(t0, 90), hs(t0.Add(2*time.Hour), 90)...), false, 0, 0, 0},
		{"unsorted input", []HourScore{{t0.Add(time.Hour), 80}, {t0, 80}}, true, 0, 2, 80},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, ok := BestWindow(tc.in, MinHourScore, MinWindowHours, MaxWindowHours)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (%+v)", ok, tc.ok, w)
			}
			if !ok {
				return
			}
			if !w.Start.Equal(t0.Add(time.Duration(tc.startH)*time.Hour)) || w.End.Sub(w.Start) != time.Duration(tc.lenH)*time.Hour {
				t.Fatalf("window %v–%v, want start +%dh len %dh", w.Start, w.End, tc.startH, tc.lenH)
			}
			if w.Mean != tc.mean {
				t.Fatalf("mean %v want %v", w.Mean, tc.mean)
			}
		})
	}
}

func TestBestWindowDoesNotMutateInput(t *testing.T) {
	in := []HourScore{{t0.Add(time.Hour), 80}, {t0, 80}}
	BestWindow(in, 60, 2, 0)
	if !in[0].Start.Equal(t0.Add(time.Hour)) {
		t.Fatal("input reordered")
	}
}

func TestIsSendTime(t *testing.T) {
	d := func(y int, m time.Month, day int) *time.Time {
		v := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
		return &v
	}
	thu := int(time.Thursday)
	cases := []struct {
		name string
		now  time.Time
		tz   string
		last *time.Time
		ok   bool
		date time.Time
	}{
		// 2026-11-19 is a Thursday.
		{"UTC thursday 18:00", time.Date(2026, 11, 19, 18, 5, 0, 0, time.UTC), "UTC", nil, true, *d(2026, 11, 19)},
		{"unknown tz falls back to UTC", time.Date(2026, 11, 19, 18, 5, 0, 0, time.UTC), "", nil, true, *d(2026, 11, 19)},
		{"invalid tz falls back to UTC", time.Date(2026, 11, 19, 18, 5, 0, 0, time.UTC), "Not/AZone", nil, true, *d(2026, 11, 19)},
		{"wrong hour", time.Date(2026, 11, 19, 17, 59, 0, 0, time.UTC), "UTC", nil, false, *d(2026, 11, 19)},
		{"wrong weekday", time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC), "UTC", nil, false, *d(2026, 11, 20)},
		{"already sent today", time.Date(2026, 11, 19, 18, 30, 0, 0, time.UTC), "UTC", d(2026, 11, 19), false, *d(2026, 11, 19)},
		{"sent last week", time.Date(2026, 11, 19, 18, 30, 0, 0, time.UTC), "UTC", d(2026, 11, 12), true, *d(2026, 11, 19)},
		// Amsterdam is UTC+1 in November: 17:10Z = 18:10 local.
		{"amsterdam offset", time.Date(2026, 11, 19, 17, 10, 0, 0, time.UTC), "Europe/Amsterdam", nil, true, *d(2026, 11, 19)},
		{"amsterdam: 18:10Z is 19:10 local", time.Date(2026, 11, 19, 18, 10, 0, 0, time.UTC), "Europe/Amsterdam", nil, false, *d(2026, 11, 19)},
		// Auckland UTC+13 (NZDT): Thu 18:00 local = Thu 05:00Z.
		{"auckland ahead of UTC", time.Date(2026, 11, 19, 5, 0, 0, 0, time.UTC), "Pacific/Auckland", nil, true, *d(2026, 11, 19)},
		// Same instant is still Wednesday in UTC but Thursday in Auckland.
		{"auckland date differs from utc date", time.Date(2026, 11, 19, 5, 0, 0, 0, time.UTC), "UTC", nil, false, *d(2026, 11, 19)},
		// Honolulu UTC-10: Fri 04:00Z = Thu 18:00 local, local date is the 19th.
		{"honolulu behind UTC", time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC), "Pacific/Honolulu", nil, true, *d(2026, 11, 19)},
		{"honolulu already sent same local date", time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC), "Pacific/Honolulu", d(2026, 11, 19), false, *d(2026, 11, 19)},
		// Kolkata UTC+5:30: 12:30Z = 18:00 local; 12:00Z = 17:30 local.
		{"half-hour zone hit", time.Date(2026, 11, 19, 12, 30, 0, 0, time.UTC), "Asia/Kolkata", nil, true, *d(2026, 11, 19)},
		{"half-hour zone miss", time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC), "Asia/Kolkata", nil, false, *d(2026, 11, 19)},
		// DST: US fall-back 2026-11-01 (Sunday) — Thursday after is plain EST (UTC-5).
		{"new york after DST end", time.Date(2026, 11, 19, 23, 0, 0, 0, time.UTC), "America/New_York", nil, true, *d(2026, 11, 19)},
		{"new york 22:00Z would have been EDT hour", time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC), "America/New_York", nil, false, *d(2026, 11, 19)},
		// Local day rolls over: Fri 00:30Z in New York is still Thu 19:30.
		{"new york local date stays thursday", time.Date(2026, 11, 20, 0, 30, 0, 0, time.UTC), "America/New_York", nil, false, *d(2026, 11, 19)},
		// lastSentOn carrying a non-midnight / non-UTC time of the same date.
		{"lastSentOn with clock part", time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC), "UTC", func() *time.Time {
			v := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
			return &v
		}(), false, *d(2026, 11, 19)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			date, ok := IsSendTime(tc.now, tc.tz, thu, 18, tc.last)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if !date.Equal(tc.date) {
				t.Fatalf("local date %v want %v", date, tc.date)
			}
		})
	}
}

func TestWeekendDates(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		today time.Time
		want  []time.Time
	}{
		{d(19), []time.Time{d(21), d(22)}}, // Thursday
		{d(14), []time.Time{d(14), d(15)}}, // Saturday
		{d(15), []time.Time{d(15)}},        // Sunday: only today
		{d(16), []time.Time{d(21), d(22)}}, // Monday
	}
	for _, tc := range cases {
		got := WeekendDates(tc.today)
		if len(got) != len(tc.want) {
			t.Fatalf("%v: got %v", tc.today, got)
		}
		for i := range got {
			if !got[i].Equal(tc.want[i]) {
				t.Fatalf("%v: got %v want %v", tc.today, got, tc.want)
			}
		}
	}
}

func TestDayRangeDST(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Amsterdam")
	// 2026-10-25 is the DST end day; the shift (03:00) precedes the range, so 12h.
	from, to := DayRange(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), loc)
	if from.Hour() != 8 || to.Hour() != 20 || to.Sub(from) != 12*time.Hour {
		t.Fatalf("from=%v to=%v", from, to)
	}
}
