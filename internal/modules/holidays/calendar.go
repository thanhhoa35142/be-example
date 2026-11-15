package holidays

import (
	"sort"
	"time"

	"github.com/rickar/cal/v2"
	"github.com/rickar/cal/v2/at"
	"github.com/rickar/cal/v2/au"
	"github.com/rickar/cal/v2/be"
	"github.com/rickar/cal/v2/ch"
	"github.com/rickar/cal/v2/cz"
	"github.com/rickar/cal/v2/de"
	"github.com/rickar/cal/v2/dk"
	"github.com/rickar/cal/v2/es"
	"github.com/rickar/cal/v2/fr"
	"github.com/rickar/cal/v2/gb"
	"github.com/rickar/cal/v2/ie"
	"github.com/rickar/cal/v2/it"
	"github.com/rickar/cal/v2/lu"
	"github.com/rickar/cal/v2/nl"
	"github.com/rickar/cal/v2/pl"
	"github.com/rickar/cal/v2/pt"
	"github.com/rickar/cal/v2/se"
	"github.com/rickar/cal/v2/us"
)

type Day struct {
	Date time.Time
	Name string
}

var national = map[string][]*cal.Holiday{
	"AT": at.Holidays,
	"BE": be.Holidays,
	"CZ": cz.Holidays,
	"DE": de.Holidays,
	"DK": dk.Holidays,
	"ES": es.Holidays,
	"FR": fr.Holidays,
	"GB": gb.Holidays,
	"IE": ie.Holidays,
	"IT": it.Holidays,
	"LU": lu.Holidays,
	"NL": nl.Holidays,
	"PL": pl.Holidays,
	"PT": pt.Holidays,
	"SE": se.Holidays,
	"US": us.Holidays,

	"CH": withoutHolidays(ch.Holidays, chCantonal()),
}

var auStates = [][]*cal.Holiday{
	au.HolidaysACT, au.HolidaysNSW, au.HolidaysNT, au.HolidaysQLD,
	au.HolidaysSA, au.HolidaysTAS, au.HolidaysVIC, au.HolidaysWA,
}

func chCantonal() []*cal.Holiday {
	return []*cal.Holiday{
		ch.Berchtoldstag, ch.HeiligeDreiKoenige, ch.Josefstag,
		ch.Fronleichnam, ch.MariaHimmelfahrt, ch.MariaEmpfangnis,
	}
}

func withoutHolidays(list, drop []*cal.Holiday) []*cal.Holiday {
	skip := make(map[*cal.Holiday]bool, len(drop))
	for _, h := range drop {
		skip[h] = true
	}
	out := make([]*cal.Holiday, 0, len(list))
	for _, h := range list {
		if !skip[h] {
			out = append(out, h)
		}
	}
	return out
}

func Covered(country string) bool {
	if country == "AU" {
		return true
	}
	_, ok := national[country]
	return ok
}

func CoveredCountries() []string {
	out := []string{"AU"}
	for c := range national {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func civil(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func counts(h *cal.Holiday) bool { return h.Type != cal.ObservanceOther }

func For(country string, year int) (days []Day, ok bool) {
	if country == "AU" {
		return australia(year), true
	}
	list, ok := national[country]
	if !ok {
		return nil, false
	}
	return collect(list, year), true
}

func collect(list []*cal.Holiday, year int) []Day {
	byDate := map[time.Time]string{}
	for _, h := range list {
		if !counts(h) {
			continue
		}
		_, observed := h.Calc(year)
		if observed.IsZero() {
			continue
		}
		d := civil(observed)
		if d.Year() != year {
			continue
		}
		if _, dup := byDate[d]; !dup {
			byDate[d] = h.Name
		}
	}
	return sorted(byDate)
}

func australia(year int) []Day {
	var perState []map[time.Time]string
	for _, list := range auStates {
		m := map[time.Time]string{}
		for _, d := range collect(list, year) {
			m[d.Date] = d.Name
		}
		perState = append(perState, m)
	}
	common := map[time.Time]string{}
	for date, name := range perState[1] {
		all := true
		for _, m := range perState {
			if _, has := m[date]; !has {
				all = false
				break
			}
		}
		if all {
			common[date] = name
		}
	}
	return sorted(common)
}

func sorted(byDate map[time.Time]string) []Day {
	out := make([]Day, 0, len(byDate))
	for d, n := range byDate {
		out = append(out, Day{Date: d, Name: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}
