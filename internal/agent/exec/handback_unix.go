//go:build unix

package exec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// handBack makes every directory of the workspace at dir that the run
// made writable by every user, when the workspace is not the runner's
// own. A launcher that cannot change owners, one that is not root, hands
// the runner a workspace every user may write, and removes it once the
// run pauses or ends, before it restores a confirmed run's saved
// workspace into the same place; removing an entry needs write access to
// its directory, which a directory the run made at 0755 denies the
// launcher's user. Only that user and the run's container reach the
// workspace, through the launcher's cache directory.
//
// ref: decision 0008 § "The run specification and its secrets"
func handBack(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err //nolint:wrapcheck // names the path
	}
	if owner(fi) == os.Getuid() {
		return nil
	}
	return openUp(dir, os.Getuid())
}

// openUp sets every directory below dir that uid owns to 0777, through
// an os.Root, so no link leads it out of dir. A directory it cannot read
// or change is skipped, and the errors are joined.
func openUp(dir string, uid int) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err //nolint:wrapcheck // names the path
	}
	defer root.Close()
	var errs []error
	_ = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			errs = append(errs, err)
			return nil
		case path == "." || !d.IsDir():
			return nil
		}
		fi, err := d.Info()
		if err == nil && owner(fi) == uid {
			err = root.Chmod(filepath.FromSlash(path), 0o777)
		}
		if err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	return errors.Join(errs...)
}

// owner is the UID that owns the file fi describes, or -1 when the
// system names none.
func owner(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
