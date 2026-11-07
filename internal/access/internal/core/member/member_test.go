package member_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
)

func TestUnitCheckEmail(t *testing.T) {
	for _, ok := range []string{"", "bob@example.com"} {
		if err := member.CheckEmail("email", ok); err != nil {
			t.Errorf("CheckEmail(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"bob", "Bob <bob@example.com>", "bob@"} {
		err := member.CheckEmail("email", bad)
		var fe *organization.FieldError
		if !errors.As(err, &fe) || fe.Field != "email" {
			t.Errorf("CheckEmail(%q) = %v, want a FieldError naming email", bad, err)
		}
	}
}
