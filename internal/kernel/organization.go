package kernel

// OrganizationID identifies an organization: a customer's agreement with
// the installation, which holds its members and one or more accounts.
// Root, the installation's top node, has no row and so no ID; only
// operator principals act on it.
//
// ref: decision 0007
type OrganizationID struct{ ULID }

// NewOrganizationID returns a new organization's ID.
func NewOrganizationID() OrganizationID { return OrganizationID{NewULID()} }

// ParseOrganizationID reads the 26-character wire form.
func ParseOrganizationID(s string) (OrganizationID, error) {
	u, err := ParseULID(s)
	return OrganizationID{u}, err
}

// MemberID identifies a member: a person in an organization, however many
// of its accounts admit them.
//
// ref: decision 0006
type MemberID struct{ ULID }

// NewMemberID returns a new member's ID.
func NewMemberID() MemberID { return MemberID{NewULID()} }

// ParseMemberID reads the 26-character wire form.
func ParseMemberID(s string) (MemberID, error) {
	u, err := ParseULID(s)
	return MemberID{u}, err
}
