package settings

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

var (
	// ErrInvalid reports a value a setting refuses; the error names it.
	ErrInvalid = errors.New("invalid account setting")
	// ErrInvalidState reports an account deletion its state refuses: the
	// account is not marked, or is its organization's last.
	ErrInvalidState = errors.New("invalid account state")
	// ErrNotHuman reports a key or a run changing a setting only a person
	// may change.
	ErrNotHuman = errors.New("only a person may change this setting")
)

// Settings are an account's settings that no other domain owns. An account
// that never changed one reads Defaults.
type Settings struct {
	// PersonalAPIKeys is whether the account's members may create personal
	// API keys.
	PersonalAPIKeys bool
	// NonHumanKeyManagers is whether API keys may manage API keys.
	NonHumanKeyManagers bool
	// NonAdminKeyManagers is apiKeyEnableNonAdminManagers' setting.
	NonAdminKeyManagers bool
	SecurityEmail       string
	// RedirectHosts is the OAuth redirect allowlist, in the order set.
	RedirectHosts []string
	Survey        Survey
	// Mark is the deletion mark, nil while the account is not marked.
	Mark *Mark
	// LastAccount is whether the account is its organization's only one,
	// which no mark lets anyone delete.
	LastAccount bool
}

// Defaults are the settings of an account that never changed one.
func Defaults() Settings {
	return Settings{PersonalAPIKeys: true, RedirectHosts: []string{}}
}

// Deletable reports whether accountConfirmDelete would delete the account
// now. No waiting period follows the mark.
func (s Settings) Deletable() bool { return s.Mark != nil && !s.LastAccount }

// Survey is the account's answer to how it heard of the product.
type Survey struct {
	Completed bool
	// Response is nil when none was given.
	Response *string
}

// Mark is an account's deletion mark.
type Mark struct {
	At          time.Time
	Reason      *string
	Description *string
}

// CheckRedirectHosts checks each entry of decision 0006's allowlist: a bare
// host, or a host after one leading "*.", which matches its subdomains. A
// scheme, port, path, user or any other character fails, naming the
// entry, as does an entry given twice. Hosts compare without case, so
// each is returned in lower case.
func CheckRedirectHosts(hosts []string) ([]string, error) {
	out := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	var errs []error
	for i, h := range hosts {
		l := strings.ToLower(h)
		switch {
		case !host(strings.TrimPrefix(l, "*.")):
			errs = append(errs, fmt.Errorf("%w: hosts[%d] %q is not a host or a *. host",
				ErrInvalid, i, h))
		case seen[l]:
			errs = append(errs, fmt.Errorf("%w: hosts[%d] %q is given twice", ErrInvalid, i, h))
		}
		seen[l] = true
		out = append(out, l)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// host reports whether s is a DNS name: dot-separated labels of letters,
// digits and inner hyphens, each at most 63 bytes, 253 in all. A "*" left
// after the prefix is not a label, so "*.*.x" and "x.*" fail.
func host(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// CheckSecurityEmail checks an address: a bare e-mail address, or empty,
// which clears it.
func CheckSecurityEmail(email string) error {
	if email == "" {
		return nil
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return fmt.Errorf("%w: securityEmail %q is not an e-mail address", ErrInvalid, email)
	}
	return nil
}
