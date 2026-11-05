package cron

import "time"

// searchYears bounds Next's search. Parse rejects a schedule that never
// fires, and the rarest one that does, 29 February alone, recurs within
// eight years (2096 to 2104).
const searchYears = 9

// Next is the first instant after after at which s fires in loc; a nil loc
// is UTC. The schedule matches local wall-clock minutes. A day matches when
// its month matches and, if day of month and day of week are both
// restricted, either of them matches (Vixie cron's rule); otherwise both
// must, which leaves the restricted one deciding. A field is restricted
// when it leaves out any of its values, so `*/2` in day of month is
// restricted and `0-7` in day of week is not.
//
// A wall-clock time that a spring-forward change skips fires once, at the
// first instant after the gap, however many skipped times match. A time
// that an autumn change repeats fires at its first occurrence only. Next
// returns the zero Time only for a Schedule that Parse did not make.
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := after.In(loc)
	// Civil dates are carried as UTC midnights, so adding a day never
	// meets a zone change.
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	end := day.AddDate(searchYears, 0, 0)
	from := local.Hour()*60 + local.Minute() + 1
	for ; day.Before(end); day, from = day.AddDate(0, 0, 1), 0 {
		if !s.matchesDay(day) {
			continue
		}
		for m := from; m < 24*60; m++ {
			if s.fields[hour].bits&(1<<(m/60)) == 0 || s.fields[minute].bits&(1<<(m%60)) == 0 {
				continue
			}
			if t := resolve(day, m, loc); t.After(after) {
				return t
			}
		}
	}
	return time.Time{}
}

// NextOf is the first instant after after at which any of schedules fires
// in loc, so a schedule of several expressions fires once at an instant
// that more than one matches. It is the zero Time for no schedules.
func NextOf(schedules []Schedule, after time.Time, loc *time.Location) time.Time {
	var next time.Time
	for _, s := range schedules {
		if t := s.Next(after, loc); !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	return next
}

func (s Schedule) matchesDay(day time.Time) bool {
	if s.fields[month].bits&(1<<int(day.Month())) == 0 {
		return false
	}
	dom := s.fields[dayOfMonth].bits&(1<<day.Day()) != 0
	dow := s.fields[dayOfWeek].bits&(1<<int(day.Weekday())) != 0
	if s.restricted(dayOfMonth) && s.restricted(dayOfWeek) {
		return dom || dow
	}
	return dom && dow
}

// resolve is the instant the wall-clock minute m of day reads in loc: its
// first occurrence when an autumn change repeats it, and the end of the
// gap when a spring change skips it.
func resolve(day time.Time, m int, loc *time.Location) time.Time {
	wall := day.Unix() + int64(m)*60
	// The offsets in force a day either side of the wall time include
	// both sides of any change near it. An offset applies when the instant
	// it gives reads the same offset.
	var (
		first int64
		found bool
	)
	for _, probe := range [...]int64{wall - 86400, wall, wall + 86400} {
		_, off := time.Unix(probe, 0).In(loc).Zone()
		t := wall - int64(off)
		if _, o := time.Unix(t, 0).In(loc).Zone(); o == off && (!found || t < first) {
			first, found = t, true
		}
	}
	if found {
		return time.Unix(first, 0).In(loc)
	}
	// Read with the offset from before the gap, the wall time is an instant
	// just after the change, whose zone starts at the change.
	_, before := time.Unix(wall-86400, 0).In(loc).Zone()
	start, _ := time.Unix(wall-int64(before), 0).In(loc).ZoneBounds()
	return start
}
