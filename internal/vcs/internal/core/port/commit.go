package port

import "encoding/hex"

// ValidCommit accepts a Git object identity, excluding the zero deletion marker.
// Empty local-workspace commit values must never join repository head history.
func ValidCommit(hash string) bool {
	if len(hash) != 40 && len(hash) != 64 || ZeroCommit(hash) {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}
