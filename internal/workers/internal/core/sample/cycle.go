package sample

import "time"

// MaxCyclesAgo is the oldest billing cycle a usage read selects, counted
// back from the current one.
const MaxCyclesAgo = 5

// Cycle is a billing cycle, a calendar month in UTC: CyclesAgo cycles
// before the current one, from Start up to, not including, End.
type Cycle struct {
	CyclesAgo  int
	Start, End time.Time
}

// Cycles are the billing cycles a usage read may select now, newest
// first: the current cycle and up to MaxCyclesAgo before it, stopping at
// the oldest cycle that still holds a minute the samples' retention keeps.
func (s *Service) Cycles() []Cycle {
	now := s.now().UTC()
	oldest := now.Add(-s.retention)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := []Cycle{{Start: start, End: start.AddDate(0, 1, 0)}}
	for n := 1; n <= MaxCyclesAgo; n++ {
		c := Cycle{CyclesAgo: n, Start: start.AddDate(0, -n, 0), End: start.AddDate(0, 1-n, 0)}
		if !c.End.After(oldest) {
			break
		}
		out = append(out, c)
	}
	return out
}
