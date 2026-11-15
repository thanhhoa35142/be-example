package digest

import (
	"sort"
	"time"
)

func BestWindow(hours []HourScore, minScore, minHours, maxHours int) (Window, bool) {
	if minHours < 1 {
		minHours = 1
	}
	hs := append([]HourScore(nil), hours...)
	sort.Slice(hs, func(i, j int) bool { return hs[i].Start.Before(hs[j].Start) })

	var (
		best    Window
		bestSum int
		bestLen int
		found   bool
	)
	consider := func(run []HourScore) {
		n := len(run)
		prefix := make([]int, n+1)
		for i, h := range run {
			prefix[i+1] = prefix[i] + h.Score
		}
		for i := 0; i < n; i++ {
			for j := i + minHours; j <= n && (maxHours <= 0 || j-i <= maxHours); j++ {
				sum, l := prefix[j]-prefix[i], j-i
				better := !found
				if found {

					a, b := sum*bestLen, bestSum*l
					switch {
					case a != b:
						better = a > b
					case l != bestLen:
						better = l > bestLen
					default:
						better = run[i].Start.Before(best.Start)
					}
				}
				if better {
					found = true
					bestSum, bestLen = sum, l
					best = Window{
						Start: run[i].Start,
						End:   run[j-1].Start.Add(time.Hour),
						Mean:  float64(sum) / float64(l),
					}
				}
			}
		}
	}

	var run []HourScore
	for _, h := range hs {
		if h.Score < minScore {
			if len(run) >= minHours {
				consider(run)
			}
			run = run[:0]
			continue
		}
		if len(run) > 0 && !h.Start.Equal(run[len(run)-1].Start.Add(time.Hour)) {
			if len(run) >= minHours {
				consider(run)
			}
			run = run[:0]
		}
		run = append(run, h)
	}
	if len(run) >= minHours {
		consider(run)
	}
	return best, found
}

func IsSendTime(now time.Time, tz string, dow, hour int, lastSentOn *time.Time) (time.Time, bool) {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	today := civilDate(local)
	if int(local.Weekday()) != dow || local.Hour() != hour {
		return today, false
	}
	if lastSentOn != nil && civilDate(*lastSentOn).Equal(today) {
		return today, false
	}
	return today, true
}

func WeekendDates(today time.Time) []time.Time {
	today = civilDate(today)
	dow := int(today.Weekday())
	if dow == int(time.Sunday) {
		return []time.Time{today}
	}
	sat := today.AddDate(0, 0, int(time.Saturday)-dow)
	return []time.Time{sat, sat.AddDate(0, 0, 1)}
}

func DayRange(date time.Time, loc *time.Location) (time.Time, time.Time) {
	y, m, d := date.Date()
	return time.Date(y, m, d, DayStartHour, 0, 0, 0, loc), time.Date(y, m, d, DayEndHour, 0, 0, 0, loc)
}

func civilDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
