package logs

import (
	"bytes"
	"math"
	"unicode/utf8"

	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// A ring keeps memory proportional to the caller's budget even for one very
// long line. Count newlines as bytes leave the ring, without splitting or
// re-encoding the worker's masked text.
type planningTail struct {
	bytes       []byte
	start, size int
	cutLines    int
	removed     bool
	lastRemoved byte
}

func newPlanningTail(size int) *planningTail { return &planningTail{bytes: make([]byte, size)} }
func (t *planningTail) dropped(b []byte) {
	if len(b) == 0 {
		return
	}
	t.cutLines += bytes.Count(b, []byte{'\n'})
	t.removed = true
	t.lastRemoved = b[len(b)-1]
}

func (t *planningTail) drop(n int) {
	first := min(n, len(t.bytes)-t.start)
	t.dropped(t.bytes[t.start : t.start+first])
	t.dropped(t.bytes[:n-first])
	t.start = (t.start + n) % len(t.bytes)
	t.size -= n
}

func (t *planningTail) write(b []byte) {
	if len(b) >= len(t.bytes) {
		t.drop(t.size)
		t.dropped(b[:len(b)-len(t.bytes)])
		copy(t.bytes, b[len(b)-len(t.bytes):])
		t.start = 0
		t.size = len(t.bytes)
		return
	}
	if excess := t.size + len(b) - len(t.bytes); excess > 0 {
		t.drop(excess)
	}
	end := (t.start + t.size) % len(t.bytes)
	first := copy(t.bytes[end:], b)
	copy(t.bytes, b[first:])
	t.size += len(b)
}

func (t *planningTail) missing(count int64) error {
	line := Missing + "\n"
	// Large unindexed gaps need not generate or allocate every placeholder.
	// Only the repetitions that can survive in the retained tail are written.
	retained := int64(len(t.bytes)/len(line) + 1)
	if count > retained {
		t.drop(t.size)
		skipped := count - retained
		if skipped > int64(math.MaxInt-t.cutLines) {
			return errPlanningIndex
		}
		t.cutLines += int(skipped)
		t.removed = true
		t.lastRemoved = '\n'
		count = retained
	}
	for range count {
		t.write([]byte(line))
	}
	return nil
}

func (t *planningTail) result() runsapi.PlanningLog {
	data := make([]byte, t.size)
	first := copy(data, t.bytes[t.start:min(t.start+t.size, len(t.bytes))])
	copy(data[first:], t.bytes[:t.size-first])
	// The byte budget may begin inside a UTF-8 encoding; retaining its suffix
	// would corrupt an otherwise valid stored log. Drop only that split rune.
	if t.removed {
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			t.dropped(data[:1])
			data = data[1:]
		}
	}
	return runsapi.PlanningLog{Text: string(data), CutLines: t.cutLines, PartialLine: t.removed && t.lastRemoved != '\n'}
}
