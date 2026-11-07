package ctl

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// ownerOnly is the SDDL of a protected DACL that gives sid full access and
// no one else any. Protected, it inherits nothing from the directory, whose
// ACL may admit administrators and other accounts; a file mode cannot say
// this on Windows, where os.Chmod sets only the read-only attribute.
func ownerOnly(sid *windows.SID) string { return "D:P(A;;FA;;;" + sid.String() + ")" }

// private makes the file at path readable and writable by the process's
// user alone.
func private(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	sd, err := windows.SecurityDescriptorFromString(ownerOnly(user.User.Sid))
	if err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}
