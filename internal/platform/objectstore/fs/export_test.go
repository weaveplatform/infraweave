package fs

import "time"

// SetStall replaces b's moving.Stall, so a test of a stalled transfer waits less.
func SetStall(b *Bucket, d time.Duration) { b.stall = d }
