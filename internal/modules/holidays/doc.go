// Package holidays maintains t_holidays (national public holidays) from the
// offline github.com/rickar/cal/v2 calendars (BSD-3), so production needs no
// external holiday API. The crowd module reads the table (CalendarFactor).
//
// Only national rows are written (region = ”): crowd.refresh keys holidays
// by country alone, so a regional row would boost the whole country.
package holidays
