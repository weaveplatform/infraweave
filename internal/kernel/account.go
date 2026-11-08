package kernel

// AccountID identifies an account: a business entity within an
// organization, and the data boundary every account-scoped row carries in
// account_id.
//
// ref: decision 0007
type AccountID struct{ ULID }

// NewAccountID returns a new account's ID.
func NewAccountID() AccountID { return AccountID{NewULID()} }

// ParseAccountID reads the 26-character wire form.
func ParseAccountID(s string) (AccountID, error) {
	u, err := ParseULID(s)
	return AccountID{u}, err
}
