package assembly

import "github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"

// HashPassword is password's break-glass hash in the PBKDF2-SHA256 format
// breakglass parses, which infraweave auth hash-password prints.
//
// ref: decision 0006 § "Human login"
func HashPassword(password string) (string, error) {
	h, err := breakglass.New(password)
	if err != nil {
		return "", err //nolint:wrapcheck // names itself
	}
	return h.String(), nil
}

// VerifyPassword reports whether password matches hash, a hash in the
// format HashPassword produces; a hash it cannot parse matches nothing.
//
// ref: decision 0006 § "Human login"
func VerifyPassword(hash, password string) bool {
	h, err := breakglass.Parse("hash", hash)
	return err == nil && h.Verify(password)
}
