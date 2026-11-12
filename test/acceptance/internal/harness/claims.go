package harness

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// JWTPart decodes part i of a compact JWT: 0 the header, 1 the claims.
func JWTPart(token string, i int, v any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: %d parts in the token", ErrStep, len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[i])
	if err != nil {
		return fmt.Errorf("%w: token part %d: %w", ErrStep, i, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: token part %d: %w", ErrStep, i, err)
	}
	return nil
}

// Audience is aud, which RFC 7519 lets a token carry as one string or an
// array.
type Audience []string

func (a *Audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = Audience{one}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(a)) //nolint:wrapcheck // the claims' decode names it
}

type APIClaims struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience Audience `json:"aud"`
	ID       string   `json:"jti"`
	Kind     string   `json:"iw_kind"` //nolint:tagliatelle // the claim is named iw_kind on the wire
	IssuedAt int64    `json:"iat"`
	Expiry   int64    `json:"exp"`
}

func EqualClaims(a, b APIClaims) bool {
	return a.Issuer == b.Issuer && a.Subject == b.Subject && slices.Equal(a.Audience, b.Audience) &&
		a.ID == b.ID && a.Kind == b.Kind && a.IssuedAt == b.IssuedAt && a.Expiry == b.Expiry
}
