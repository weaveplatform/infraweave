package ctl

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isPrivate fails unless the file at path has a protected DACL of one
// entry, allowing the process's user the access ownerOnly grants.
func isPrivate(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	want, err := windows.SecurityDescriptorFromString(ownerOnly(user.User.Sid))
	if err != nil {
		return err
	}
	wantMask, _, err := soleAce(want)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	mask, sid, err := soleAce(sd)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", path, err, sd)
	}
	if control&windows.SE_DACL_PROTECTED == 0 || mask != wantMask || !sid.Equals(user.User.Sid) {
		return fmt.Errorf("%s has the DACL %s, want %s", path, sd, want)
	}
	return nil
}

// soleAce is the mask and SID of sd's DACL's only entry, an allow.
func soleAce(sd *windows.SECURITY_DESCRIPTOR) (windows.ACCESS_MASK, *windows.SID, error) {
	dacl, _, err := sd.DACL()
	if err != nil {
		return 0, nil, err
	}
	if dacl == nil || dacl.AceCount != 1 {
		return 0, nil, fmt.Errorf("the DACL does not hold one entry")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return 0, nil, err
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return 0, nil, fmt.Errorf(
			"the DACL's entry is of type %d, not an allow",
			ace.Header.AceType,
		)
	}
	return ace.Mask, (*windows.SID)(unsafe.Pointer(&ace.SidStart)), nil
}
