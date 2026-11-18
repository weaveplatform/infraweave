package port

import (
	"errors"
	"strings"
)

// DiffMode chooses the existing provider comparison or push-specific semantics.
type DiffMode uint8

const (
	LegacyDiff DiffMode = iota
	// PushDiff compares the complete before/after trees. A zero before
	// starts at the default branch's merge base; a zero head is a no-op.
	PushDiff
)

// ErrDiffMode refuses unsupported or conflicting comparison options.
var ErrDiffMode = errors.New("vcs: invalid affected-files comparison mode")

// Comparison preserves the existing mode when no option is supplied.
func Comparison(modes []DiffMode) (DiffMode, error) {
	if len(modes) == 0 {
		return LegacyDiff, nil
	}
	if len(modes) != 1 || modes[0] != LegacyDiff && modes[0] != PushDiff {
		return LegacyDiff, ErrDiffMode
	}
	return modes[0], nil
}

// ZeroCommit is Git's creation/deletion marker, rather than a commit to resolve.
func ZeroCommit(hash string) bool {
	return (len(hash) == 40 || len(hash) == 64) && strings.Trim(hash, "0") == ""
}
