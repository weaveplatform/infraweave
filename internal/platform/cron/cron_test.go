package cron_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/cron"
)

var accepted = []struct{ expr, canonical string }{
	{"* * * * *", "* * * * *"},
	{"  0\t0  *  *  * ", "0 0 * * *"},
	{"*/15 0-6/2 1,15 1-12 1-5", "*/15 0-6/2 1,15 1-12 1-5"},
	{"05 004 * * 0", "5 4 * * 0"},
	{"0 0 * * 7", "0 0 * * 7"},
	{"5/10 * * * *", "5/10 * * * *"},
	{"0-59/30,7 * * * *", "0-59/30,7 * * * *"},
	{"0 0 31 1,3 *", "0 0 31 1,3 *"},
	{"0 0 30 2 1", "0 0 30 2 1"},
	{"0 0 29 2 *", "0 0 29 2 *"},
	{"59 23 31 12 0-7/7", "59 23 31 12 0-7/7"},
}

var rejected = []struct{ expr, reason string }{
	{"", "got 0"},
	{"* * * *", "got 4"},
	{"0 * * * * *", "got 6"},
	{"0 0 * * * * 2026", "got 7"},
	{"@daily", "@ macros are not supported"},
	{"@every 5m", "@ macros are not supported"},
	{"0 0 * JAN *", `month: value "JAN" is not a number`},
	{"0 0 * * MON", `day of week: value "MON" is not a number`},
	{"0 0 ? * *", `day of month: value "?" is not a number`},
	{"0 0 L * *", `day of month: value "L" is not a number`},
	{"0 0 15W * *", `day of month: value "15W" is not a number`},
	{"0 0 * * 5#3", `day of week: value "5#3" is not a number`},
	{"+1 * * * *", `minute: value "+1" is not a number`},
	{"60 * * * *", "minute: 60 is outside 0-59"},
	{"* 24 * * *", "hour: 24 is outside 0-23"},
	{"* * 0 * *", "day of month: 0 is outside 1-31"},
	{"* * * 13 *", "month: 13 is outside 1-12"},
	{"* * * * 8", "day of week: 8 is outside 0-7"},
	{"99999999999999999999 * * * *", "minute: 9999 is outside 0-59"},
	{"5-1 * * * *", "minute: range 5-1 is reversed"},
	{"*/0 * * * *", "minute: step 0 is outside 1-60"},
	{"* */25 * * *", "hour: step 25 is outside 1-24"},
	{"1,,2 * * * *", "minute: empty value"},
	{"-1 * * * *", "minute: empty value"},
	{"1-2-3 * * * *", `minute: value "2-3" is not a number`},
	{"*/ * * * *", "minute: empty step"},
	{"*/x * * * *", `minute: step "x" is not a number`},
	{"*-5 * * * *", `minute: value "*" is not a number`},
	{"0 0 30 2 *", "day of month: 30 never occurs in month 2"},
	{"0 0 31 4,6,9,11 *", "day of month: 31 never occurs in month 4,6,9,11"},
}

func TestUnitParse(t *testing.T) {
	for _, c := range accepted {
		s, err := cron.Parse(c.expr)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.expr, err)
			continue
		}
		if got := s.String(); got != c.canonical {
			t.Errorf("Parse(%q).String() = %q, want %q", c.expr, got, c.canonical)
		}
		roundTrip(t, c.expr)
	}
	for _, c := range rejected {
		_, err := cron.Parse(c.expr)
		if !errors.Is(err, cron.ErrSyntax) || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("Parse(%q) = %v, want ErrSyntax with %q", c.expr, err, c.reason)
		}
	}
}

// roundTrip checks what every accepted expression must satisfy: its
// canonical form parses to the same schedule, and the schedule fires.
func roundTrip(t *testing.T, expr string) {
	t.Helper()
	s, err := cron.Parse(expr)
	if err != nil {
		if !errors.Is(err, cron.ErrSyntax) {
			t.Fatalf("Parse(%q): %v, not ErrSyntax", expr, err)
		}
		return
	}
	again, err := cron.Parse(s.String())
	if err != nil || again != s {
		t.Fatalf("Parse(%q) round trip through %q: %v, %v", expr, s.String(), again, err)
	}
	after := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if next := s.Next(after, time.UTC); !next.After(after) {
		t.Fatalf("Parse(%q).Next(%s) = %s, want a later time", expr, after, next)
	}
}

func FuzzParse(f *testing.F) {
	for _, c := range accepted {
		f.Add(c.expr)
	}
	for _, c := range rejected {
		f.Add(c.expr)
	}
	f.Fuzz(roundTrip)
}

func TestUnitNext(t *testing.T) {
	zone := func(name string) *time.Location {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name, zone, expr, after, want string
	}{
		// Europe/London: 01:00 GMT becomes 02:00 BST on 29 March 2026, and
		// 02:00 BST becomes 01:00 GMT on 25 October 2026.
		{
			"London gap",
			"Europe/London",
			"30 1 * * *",
			"2026-03-28T12:00:00Z",
			"2026-03-29T01:00:00Z",
		},
		{
			"London gap fires once",
			"Europe/London",
			"*/15 1 * * *",
			"2026-03-29T00:50:00Z",
			"2026-03-29T01:00:00Z",
		},
		{
			"London after the gap",
			"Europe/London",
			"*/15 1 * * *",
			"2026-03-29T01:00:00Z",
			"2026-03-30T00:00:00Z",
		},
		{
			"London end of the gap",
			"Europe/London",
			"0 2 * * *",
			"2026-03-28T12:00:00Z",
			"2026-03-29T01:00:00Z",
		},
		{
			"London repeat, first",
			"Europe/London",
			"30 1 * * *",
			"2026-10-24T12:00:00Z",
			"2026-10-25T00:30:00Z",
		},
		{
			"London repeat, not again",
			"Europe/London",
			"30 1 * * *",
			"2026-10-25T00:30:00Z",
			"2026-10-26T01:30:00Z",
		},
		{
			"London repeat, hourly",
			"Europe/London",
			"*/30 * * * *",
			"2026-10-25T00:45:00Z",
			"2026-10-25T02:00:00Z",
		},
		{
			"London repeat, during the second",
			"Europe/London",
			"*/30 * * * *",
			"2026-10-25T01:10:00Z",
			"2026-10-25T02:00:00Z",
		},
		// America/New_York: 02:00 EST becomes 03:00 EDT on 8 March 2026,
		// and 02:00 EDT becomes 01:00 EST on 1 November 2026.
		{
			"New York gap",
			"America/New_York",
			"30 2 * * *",
			"2026-03-07T12:00:00Z",
			"2026-03-08T07:00:00Z",
		},
		{
			"New York repeat, first",
			"America/New_York",
			"30 1 * * *",
			"2026-10-31T12:00:00Z",
			"2026-11-01T05:30:00Z",
		},
		{
			"New York repeat, not again",
			"America/New_York",
			"30 1 * * *",
			"2026-11-01T05:30:00Z",
			"2026-11-02T06:30:00Z",
		},
		// Australia/Lord_Howe shifts by 30 minutes: 02:00 +10:30 becomes
		// 02:30 +11:00 on 4 October 2026, and 02:00 +11:00 becomes 01:30
		// +10:30 on 5 April 2026.
		{
			"Lord Howe gap",
			"Australia/Lord_Howe",
			"15 2 * * *",
			"2026-10-03T00:00:00Z",
			"2026-10-03T15:30:00Z",
		},
		{
			"Lord Howe repeat, first",
			"Australia/Lord_Howe",
			"45 1 * * *",
			"2026-04-04T00:00:00Z",
			"2026-04-04T14:45:00Z",
		},
		{
			"Lord Howe repeat, not again",
			"Australia/Lord_Howe",
			"45 1 * * *",
			"2026-04-04T14:45:00Z",
			"2026-04-05T15:15:00Z",
		},
		// UTC.
		{"next minute", "UTC", "* * * * *", "2026-11-05T12:00:30Z", "2026-11-05T12:01:00Z"},
		{"strictly after", "UTC", "* * * * *", "2026-11-05T12:00:00Z", "2026-11-05T12:01:00Z"},
		{
			"step across the hour",
			"UTC",
			"*/20 * * * *",
			"2026-11-05T12:50:30Z",
			"2026-11-05T13:00:00Z",
		},
		{"new year", "UTC", "0 0 1 1 *", "2026-12-31T23:59:00Z", "2027-01-01T00:00:00Z"},
		{"leap day", "UTC", "0 0 29 2 *", "2026-01-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"weekdays", "UTC", "0 0 * * 1-5", "2026-01-03T00:00:00Z", "2026-01-05T00:00:00Z"},
		{"7 is Sunday", "UTC", "0 0 * * 7", "2026-01-01T00:00:00Z", "2026-01-04T00:00:00Z"},
		{"either day field", "UTC", "0 0 13 * 5", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"},
		{
			"either day field, by date",
			"UTC",
			"0 0 13 * 5",
			"2026-01-09T00:00:00Z",
			"2026-01-13T00:00:00Z",
		},
		{
			"stepped day of month is restricted",
			"UTC",
			"0 0 */10 * 2",
			"2026-01-07T00:00:00Z",
			"2026-01-11T00:00:00Z",
		},
		{
			"full day of week is not",
			"UTC",
			"0 0 15 * 0-7",
			"2026-01-01T00:00:00Z",
			"2026-01-15T00:00:00Z",
		},
		{"nil location", "", "0 12 * * *", "2026-11-05T12:00:00Z", "2026-11-06T12:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := cron.Parse(c.expr)
			if err != nil {
				t.Fatal(err)
			}
			var loc *time.Location
			if c.zone != "" {
				loc = zone(c.zone)
			}
			got := s.Next(at(c.after), loc)
			if !got.Equal(at(c.want)) {
				t.Errorf("%q in %s after %s = %s, want %s", c.expr, c.zone, c.after,
					got.UTC().Format(time.RFC3339), c.want)
			}
			if loc != nil && got.Location() != loc {
				t.Errorf("location %s, want %s", got.Location(), loc)
			}
		})
	}
}

func TestUnitNextOf(t *testing.T) {
	parse := func(exprs ...string) []cron.Schedule {
		out := make([]cron.Schedule, 0, len(exprs))
		for _, e := range exprs {
			s, err := cron.Parse(e)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	after := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		exprs []string
		want  time.Time
	}{
		{[]string{"0 9 * * *", "30 8 * * *"}, time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC)},
		{[]string{"0 9 * * *", "0 9 * * 4"}, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)},
		{nil, time.Time{}},
	} {
		got := cron.NextOf(parse(c.exprs...), after, time.UTC)
		if !got.Equal(c.want) {
			t.Errorf("NextOf(%q) = %s, want %s", c.exprs, got, c.want)
		}
		// One instant matched by both fires once: the following firing is
		// a day later, not the same instant again.
		if len(c.exprs) > 0 {
			if again := cron.NextOf(parse(c.exprs...), got, time.UTC); !again.After(got) {
				t.Errorf("NextOf(%q) after %s = %s", c.exprs, got, again)
			}
		}
	}
}
