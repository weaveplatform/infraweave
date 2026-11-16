package signed_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/state/internal/signed"
)

// keys derive purpose's key from the data key id names, of which active
// is the one DeriveKey uses; a key not in data is unknown.
type keys struct {
	active uint32
	data   map[uint32]string
	fail   error
}

func (k *keys) DeriveKey(purpose string) ([]byte, uint32, error) {
	if k.fail != nil {
		return nil, 0, k.fail
	}
	d, err := k.DerivedKey(purpose, k.active)
	return d, k.active, err
}

func (k *keys) DerivedKey(purpose string, id uint32) ([]byte, error) {
	d, ok := k.data[id]
	if !ok {
		return nil, errors.New("unknown data key")
	}
	return []byte(d + "/" + purpose), nil
}

func TestUnitSignVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	k := &keys{active: 1, data: map[uint32]string{1: "one"}}
	s := signed.New(k, func() time.Time { return now })
	up := signed.Token{
		Kind: signed.Upload, Account: kernel.NewAccountID(), Subject: "user:" + kernel.NewULID().String(),
		Object: kernel.NewULID(),
	}
	down := signed.Token{
		Kind: signed.Download, Account: up.Account, Subject: "api_key:x",
		Stack: kernel.NewULID(), Version: kernel.NewULID(),
	}
	for _, want := range []signed.Token{up, down} {
		raw, err := s.Sign(want, 15*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Verify(want.Kind, raw)
		want.Expires = now.Add(15 * time.Minute)
		if err != nil || got != want {
			t.Errorf("Verify(Sign(%+v)) = %+v, %v", want, got, err)
		}
		other := signed.Upload
		if want.Kind == signed.Upload {
			other = signed.Download
		}
		if _, err := s.Verify(other, raw); !errors.Is(err, signed.ErrToken) {
			t.Errorf("a %c token verified as %c: %v", want.Kind, other, err)
		}
	}

	raw, err := s.Sign(up, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// A rotation leaves the old data key retiring: the token verifies
	// until it expires, and a new one signs under the new key.
	k.active, k.data[2] = 2, "two"
	if _, err := s.Verify(signed.Upload, raw); err != nil {
		t.Errorf("a token signed before a rotation = %v", err)
	}
	rotated, err := s.Sign(up, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	delete(k.data, 1)
	if _, err := s.Verify(signed.Upload, raw); !errors.Is(err, signed.ErrToken) {
		t.Errorf("a token of a removed data key = %v", err)
	}
	if _, err := s.Verify(signed.Upload, rotated); err != nil {
		t.Errorf("a token signed after a rotation = %v", err)
	}

	now = now.Add(time.Minute)
	if _, err := s.Verify(signed.Upload, rotated); !errors.Is(err, signed.ErrToken) ||
		!strings.Contains(err.Error(), "expired") {
		t.Errorf("an expired token = %v", err)
	}
}

func TestUnitVerifyRefuses(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := signed.New(
		&keys{active: 1, data: map[uint32]string{1: "one"}},
		func() time.Time { return now },
	)
	raw, err := s.Sign(signed.Token{
		Kind: signed.Download, Account: kernel.NewAccountID(), Subject: "user:x",
		Stack: kernel.NewULID(), Version: kernel.NewULID(),
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := base64.RawURLEncoding.DecodeString(raw)
	encode := base64.RawURLEncoding.EncodeToString
	tampered := append([]byte{}, b...)
	tampered[10] ^= 1
	// The token is 101 bytes, so its last character carries two unused
	// bits; flipping the lowest leaves the bytes as they were.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	respelled := raw[:len(raw)-1] + string(alphabet[strings.IndexByte(alphabet, raw[len(raw)-1])^1])
	for name, bad := range map[string]string{
		"not base64": "!!",
		"respelled":  respelled,
		"short":      encode(b[:20]),
		"tampered":   encode(tampered),
		"truncated":  encode(append(append([]byte{}, b[:len(b)-41]...), b[len(b)-40:]...)),
		"other key":  encode(append([]byte{b[0], 0, 0, 0, 9}, b[5:]...)),
	} {
		if _, err := s.Verify(signed.Download, bad); !errors.Is(err, signed.ErrToken) {
			t.Errorf("a %s token = %v", name, err)
		}
	}
	if _, err := s.Verify('X', raw); !errors.Is(err, signed.ErrToken) {
		t.Errorf("a token verified as an unknown kind = %v", err)
	}
}

func TestUnitSignRefuses(t *testing.T) {
	s := signed.New(&keys{active: 1, data: map[uint32]string{1: "one"}}, time.Now)
	if _, err := s.Sign(signed.Token{Kind: 'X'}, time.Minute); !errors.Is(err, signed.ErrToken) {
		t.Errorf("an unknown kind = %v", err)
	}
	long := signed.Token{Kind: signed.Upload, Subject: strings.Repeat("x", 1<<16)}
	if _, err := s.Sign(long, time.Minute); !errors.Is(err, signed.ErrToken) {
		t.Errorf("a 64 KiB subject = %v", err)
	}
	failing := signed.New(&keys{fail: errors.New("no keyring")}, time.Now)
	if _, err := failing.Sign(signed.Token{Kind: signed.Upload}, time.Minute); err == nil {
		t.Error("a signer without keys signed")
	}
}
