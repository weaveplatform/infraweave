package member

import (
	"net/mail"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Status is a member's standing in its organization.
type Status string

const (
	// Active is a member who has signed in, or whom an organization admin
	// named an account admin.
	Active Status = "active"
	// Pending is a member an invitation created, until the person signs
	// in through it. A pending member holds a seat, as the invitation
	// promised one.
	Pending Status = "pending"
	// Deactivated is a member an organization admin deactivated: no
	// session, no seat.
	Deactivated Status = "deactivated"
)

// Member is a person in an organization.
type Member struct {
	OrganizationID kernel.OrganizationID
	ID             kernel.MemberID
	Login          string
	Name           string
	Email          string
	Status         Status
	CreatedAt      time.Time
	// LastSeenAt is the member's latest sign-in; nil before the first.
	LastSeenAt *time.Time
}

// CheckEmail checks an e-mail address field names: a bare address, or
// empty for none.
func CheckEmail(field, email string) error {
	if email == "" {
		return nil
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return &organization.FieldError{Field: field, Reason: "is not an e-mail address"}
	}
	return nil
}
