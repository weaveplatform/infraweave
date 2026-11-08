package organization

import (
	"errors"
	"strings"
	"testing"
)

func TestUnitParseSlug(t *testing.T) {
	for _, ok := range []string{"a", "acme", "acme-corp", "a1", "x-1-y", strings.Repeat("a", MaxSlug)} {
		if s, err := ParseSlug("slug", ok); err != nil || string(s) != ok {
			t.Errorf("ParseSlug(%q) = %q, %v", ok, s, err)
		}
	}
	for in, reason := range map[string]string{
		"":                      "is empty",
		strings.Repeat("a", 64): "is longer than 63 characters",
		"1acme":                 "does not start with a lower-case letter",
		"-acme":                 "does not start with a lower-case letter",
		"Acme":                  "does not start with a lower-case letter",
		"acme-":                 "ends with a hyphen",
		"ac--me":                "has two hyphens in a row",
		"root":                  `"root" is reserved`,
		"legacy":                `"legacy" is reserved`,
		"acme_corp":             `has '_'; only a-z, 0-9 and - are allowed`,
		"acMe":                  `has 'M'; only a-z, 0-9 and - are allowed`,
		"acmé":                  `has 'é'; only a-z, 0-9 and - are allowed`,
	} {
		_, err := ParseSlug("firstAccount.slug", in)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != "firstAccount.slug" || fe.Reason != reason {
			t.Errorf("ParseSlug(%q) = %v, want %q", in, err, reason)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseSlug(%q) does not match ErrInvalid", in)
		}
	}
}

func TestUnitCheckName(t *testing.T) {
	if err := CheckName("name", "Acme Corporation"); err != nil {
		t.Error(err)
	}
	// The limit counts characters, not bytes.
	if err := CheckName("name", strings.Repeat("é", MaxName)); err != nil {
		t.Error(err)
	}
	for in, reason := range map[string]string{
		"":                             "is empty",
		"  \t":                         "is empty",
		strings.Repeat("a", MaxName+1): "is longer than 100 characters",
	} {
		if err := CheckName("name", in); err == nil || err.Error() != "name: "+reason {
			t.Errorf("CheckName(%q) = %v, want %q", in, err, reason)
		}
	}
}

func TestUnitNew(t *testing.T) {
	o, err := New("acme", "Acme")
	if err != nil || o.ID.IsZero() || o.Slug != "acme" || o.Name != "Acme" {
		t.Errorf("New = %+v, %v", o, err)
	}
	// Every broken field is reported, not only the first.
	_, err = New("Root", "")
	if err == nil || !strings.Contains(err.Error(), "slug: ") ||
		!strings.Contains(err.Error(), "name: ") {
		t.Errorf("New(Root, \"\") = %v, want both fields named", err)
	}
}
