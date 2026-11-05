package kek_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

func encoded(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func TestUnitLocalWrap(t *testing.T) {
	p, err := kek.New(kek.Config{Provider: kek.Local, Local: kek.LocalConfig{Key: encoded(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref() != "local" {
		t.Errorf("Ref = %q", p.Ref())
	}
	data := bytes.Repeat([]byte{9}, 32)
	wrapped, err := p.Wrap(t.Context(), 3, data)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Unwrap(t.Context(), 3, wrapped); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %x, %v", got, err)
	}
	other, err := kek.New(kek.Config{Provider: kek.Local, Local: kek.LocalConfig{Key: encoded(2)}})
	if err != nil {
		t.Fatal(err)
	}
	version := bytes.Clone(wrapped)
	version[0] = 2
	for name, tc := range map[string]struct {
		p       kek.Provider
		id      uint32
		wrapped []byte
	}{
		"wrong KEK":       {other, 3, wrapped},
		"another key ID":  {p, 4, wrapped},
		"unknown version": {p, 3, version},
		"empty":           {p, 3, nil},
		"truncated":       {p, 3, wrapped[:20]},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.p.Unwrap(
				t.Context(),
				tc.id,
				tc.wrapped,
			); !errors.Is(
				err,
				kek.ErrUnwrap,
			) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestUnitLocalKeyFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "kek")
	if err := os.WriteFile(good, []byte(encoded(1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := kek.New(kek.Config{Provider: kek.Local, Local: kek.LocalConfig{KeyFile: good}})
	if err != nil {
		t.Fatal(err)
	}
	fromEnv, err := kek.New(
		kek.Config{Provider: kek.Local, Local: kek.LocalConfig{Key: encoded(1)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := fromFile.Wrap(t.Context(), 1, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fromEnv.Unwrap(t.Context(), 1, wrapped); err != nil {
		t.Errorf("the file's KEK and the variable's differ: %v", err)
	}
}

func TestUnitConfig(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	for name, tc := range map[string]struct {
		c    kek.Config
		want string
	}{
		"unknown provider": {kek.Config{Provider: "awskms"}, `secrets.kek.provider must be local, got "awskms"`},
		"no key":           {kek.Config{Provider: kek.Local}, "secrets.kek.local.key_file or INFRAWEAVE_SECRETS_KEK is required"},
		"both keys": {
			kek.Config{Provider: kek.Local, Local: kek.LocalConfig{KeyFile: "f", Key: encoded(1)}},
			"not both",
		},
		"not base64":   {kek.Config{Provider: kek.Local, Local: kek.LocalConfig{Key: "!!"}}, "not base64"},
		"16 bytes":     {kek.Config{Provider: kek.Local, Local: kek.LocalConfig{Key: short}}, "is 16 bytes, want 32"},
		"missing file": {kek.Config{Provider: kek.Local, Local: kek.LocalConfig{KeyFile: "/no/such/kek"}}, "key_file"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kek.New(tc.c)
			if !errors.Is(err, kek.ErrConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	if err := (kek.Config{}).Validate(); err != nil {
		t.Errorf("no provider is a valid shape: %v", err)
	}
	if _, err := kek.New(kek.Config{}); !errors.Is(err, kek.ErrNoProvider) {
		t.Errorf("no provider: %v", err)
	}
	if _, err := kek.NewLocal(make([]byte, 31)); !errors.Is(err, kek.ErrConfig) {
		t.Errorf("31-byte KEK: %v", err)
	}
}
