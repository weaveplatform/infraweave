package workspace

import "io"

// SealRaw writes raw, as the archive a saved workspace holds, sealed as
// Write seals one, so a test can craft what no Write writes.
func SealRaw(w io.Writer, o Options, raw []byte) error {
	return sealed(w, o, func(zw io.Writer) error {
		_, err := zw.Write(raw)
		return err //nolint:wrapcheck // a test seam
	})
}

// SetMaxSize sets the bound Write holds a workspace to, for one test.
func SetMaxSize(n int64) func() {
	old := maxSize
	maxSize = n
	return func() { maxSize = old }
}
