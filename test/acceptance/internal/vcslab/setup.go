package vcslab

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/weaveplatform/infraweave/internal/platform/private"
)

// WriteAppSetup writes the one-time secret to a newly created private file.
// Neither an existing file nor a symlink may be replaced. The returned readiness
// value is public metadata; it is not evidence of GitHub's configured secret.
func WriteAppSetup(path, appID string, encoded []byte, output io.Writer) error {
	key, err := DeriveAppKey(appID, encoded)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("%w: create new private setup file", ErrHook)
	}
	if err := private.Restrict(path); err != nil {
		_ = f.Close()
		return fmt.Errorf("%w: restrict private setup file", ErrHook)
	}
	_, writeErr := io.WriteString(f, key.Secret()+"\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		var cleanup error
		if err := os.Remove(path); err != nil {
			cleanup = fmt.Errorf("%w: remove incomplete private setup file", ErrHook)
		}
		return errors.Join(fmt.Errorf("%w: write private setup file", ErrHook), cleanup)
	}
	_, err = fmt.Fprintf(
		output,
		"Public key fingerprint: %s\nSetup readiness: %s\nThe derived secret is only in the requested private file. Configure the dedicated App manually, then record readiness.\n",
		key.Fingerprint,
		key.Readiness,
	)
	if err != nil {
		return fmt.Errorf("%w: report setup metadata", ErrHook)
	}
	return nil
}
