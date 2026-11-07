package private

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS, the mask SDDL's FA grants: the
// standard rights, SYNCHRONIZE and every file-specific right (0x1FF).
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF

// owner is the process's user and a protected DACL that gives it full
// access and no one else any. Protected, the DACL inherits nothing from
// the directory, whose ACL may admit administrators and other accounts.
func owner() (*windows.SID, *windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, fmt.Errorf("read the process's user: %w", err)
	}
	sid := user.User.Sid
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + sid.String() + ")")
	var dacl *windows.ACL
	if err == nil {
		dacl, _, err = sd.DACL()
	}
	return sid, dacl, err //nolint:wrapcheck // Restrict and Check wrap it with the path
}

// Restrict makes the file at path readable and writable by the process's
// user alone.
func Restrict(path string) error {
	_, dacl, err := owner()
	if err == nil {
		err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil, nil, dacl, nil)
	}
	if err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}

// Check fails unless the file at path has the DACL Restrict sets: protected,
// with one entry, allowing the process's user full access.
func Check(path string) error {
	sid, _, err := owner()
	var sd *windows.SECURITY_DESCRIPTOR
	if err == nil {
		sd, err = windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION)
	}
	if err != nil {
		return fmt.Errorf("check %s: %w", path, err)
	}
	if !ownerOnly(sd, sid) {
		return fmt.Errorf("%w: %s has the DACL %s", ErrShared, path, sd)
	}
	return nil
}

// ownerOnly reports whether sd's DACL is protected and holds one entry, an
// allow of full access for sid.
func ownerOnly(sd *windows.SECURITY_DESCRIPTOR, sid *windows.SID) bool {
	control, _, err := sd.Control()
	dacl, _, derr := sd.DACL()
	if err != nil || derr != nil || control&windows.SE_DACL_PROTECTED == 0 || dacl == nil ||
		dacl.AceCount != 1 {
		return false
	}
	// An ACCESS_ALLOWED_ACE's SID starts at SidStart and runs to the end
	// of the entry, as the Windows ACE layout defines it.
	var ace *windows.ACCESS_ALLOWED_ACE
	return windows.GetAce(dacl, 0, &ace) == nil &&
		ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && ace.Mask == fileAllAccess &&
		(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) //nolint:gosec // the ACE layout
}
