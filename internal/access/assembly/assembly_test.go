package assembly

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUnitAssembly(t *testing.T) {
	if s := Stream(); s.Name != "access" || s.Files == nil {
		t.Errorf("Stream = %+v", s)
	}
	if len(SealedColumns()) == 0 {
		t.Error("the access stream seals signing keys, but lists no sealed column")
	}
	d := New(Deps{Settings: DefaultSettings(), PublicURL: "https://iw.example.com"})
	if d == nil {
		t.Fatal("New returned nil")
	}
	_ = d.API()
	_ = (*Domain)(nil).API()
	if d.Authenticator() == nil {
		t.Error("no authenticator")
	}
	tasks := d.Cron()
	if len(tasks) != 2 || tasks[0].Kind.Name != "authn.sessions.expire" ||
		tasks[0].Every != time.Hour || tasks[1].Kind.Name != "authn.keys.rotate" ||
		tasks[1].Every != 24*time.Hour {
		t.Errorf("Cron = %+v, want authn.sessions.expire hourly and authn.keys.rotate daily", tasks)
	}
	manual := DefaultSettings()
	manual.Signing.Rotation = RotationManual
	manual.APIIssuer = "https://issuer.example.com"
	if tasks := New(Deps{Settings: manual, Now: time.Now}).Cron(); len(tasks) != 1 ||
		tasks[0].Kind != ExpireSessions {
		t.Errorf("manual rotation registers %+v", tasks)
	}
}

func TestUnitSettings(t *testing.T) {
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(*Settings)
		want string
	}{
		"ttl":         {func(s *Settings) { s.Tokens.API.TTL = -time.Second }, "auth.tokens.api.ttl"},
		"backend":     {func(s *Settings) { s.Signing.Backend = "kms" }, "auth.signing.backend"},
		"rotation":    {func(s *Settings) { s.Signing.Rotation = "" }, "auth.signing.rotation"},
		"relative":    {func(s *Settings) { s.APIIssuer = "/issuer" }, "auth.api_issuer"},
		"credentials": {func(s *Settings) { s.APIIssuer = "https://u:p@iw.example.com" }, "auth.api_issuer"},
		"query":       {func(s *Settings) { s.APIIssuer = "https://iw.example.com?x=1" }, "auth.api_issuer"},
	} {
		s := DefaultSettings()
		tc.edit(&s)
		err := s.Validate()
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate = %v, want ErrConfig naming %s", name, err, tc.want)
		}
	}
	s := DefaultSettings()
	s.APIIssuer = "https://iw.example.com/issuer"
	if err := s.Validate(); err != nil {
		t.Errorf("an issuer with a path: %v", err)
	}
}
