package mask_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// sample joins parts, so the source holds no token of a real shape for a
// secret scanner to flag.
func sample(parts ...string) string { return strings.Join(parts, "") }

// TestUnitMaskPatterns masks a sample of every well-known pattern, only
// when the Masker is asked to, and leaves each pattern's near misses
// visible.
func TestUnitMaskPatterns(t *testing.T) {
	alnum := func(n int) string { return strings.Repeat("aB3", n)[:n] }
	cases := map[string]struct {
		text, want string
		near       []string
	}{
		"aws-access-key-id": {
			text: sample("id=AK", "IAIOSFODNN7EXAMPLE and AS", "IAIOSFODNN7EXAMPLE"),
			want: "id=***** and *****",
			near: []string{
				sample("AK", "IAIOSFODNN7EXAMPL"), sample("AK", "IAIOSFODNN7EXAMPLEX"),
				sample("ak", "iaiosfodnn7example"), sample("AK", "IAIOSFODNN1EXAMPLE"),
			},
		},
		"entra-id-client-secret": {
			text: sample("secret: abc8Q", "~", alnum(30), "_~.-"),
			want: "secret: *****",
			near: []string{
				sample("abc8Q-", alnum(34)), sample("abc8Q", "~", alnum(30)),
				sample("abc8Q", "~", alnum(40)),
			},
		},
		"azure-connection-string": {
			text: sample("DefaultEndpointsProtocol=https;AccountName=dev;AccountKey=",
				alnum(40), "==;EndpointSuffix=core.windows.net"),
			want: "DefaultEndpointsProtocol=https;AccountName=dev;AccountKey=*****;" +
				"EndpointSuffix=core.windows.net",
			near: []string{
				"AccountName=devstoreaccount1withalongname;AccountKey=short;",
				sample("SharedAccessKeyName=", alnum(30)),
			},
		},
		"github-pat-classic": {
			text: sample("token ", "ghp", "_", alnum(36), "."), want: "token *****.",
			near: []string{sample("ghp", "_", alnum(35)), sample("xghp", "_", alnum(36))},
		},
		"github-pat-fine-grained": {
			text: sample("github", "_pat_", alnum(22), "_", alnum(59)), want: "*****",
			near: []string{sample("github", "_pat_", alnum(81))},
		},
		"github-app-token": {
			text: sample("ghs", "_", alnum(36), " ghu", "_", alnum(36)), want: "***** *****",
			near: []string{sample("ghz", "_", alnum(36))},
		},
		"github-refresh-token": {
			text: sample("ghr", "_", alnum(76)), want: "*****",
			near: []string{sample("ghr", "_", alnum(20))},
		},
		"github-oauth-token": {
			text: sample(`"gho`, "_", alnum(36), `"`), want: `"*****"`,
			near: []string{sample("gho", "_", alnum(36), "_x")},
		},
		"jwt": {
			text: sample("Bearer eyJ", "hbGciOiJIUzI1NiJ9.eyJ", "zdWIiOiIxMjM0In0.c2lnLW5hdHVyZQ"),
			want: "Bearer *****",
			near: []string{"eyJhbGciOiJIUzI1NiJ9.notapayload", "eyJhbGciOiJIUzI1NiJ9"},
		},
		"slack-token": {
			text: sample(
				"xox",
				"b-1234567890-",
				alnum(24),
				" and xa",
				"pp-1-A0B1C2D3E4-",
				alnum(8),
			),
			want: "***** and *****",
			near: []string{sample("xox", "b-123"), sample("xox", "q-1234567890abcdef")},
		},
	}
	names := mask.PatternNames()
	for _, name := range names {
		if _, ok := cases[name]; !ok {
			t.Errorf("the pattern %s has no sample", name)
		}
	}
	plain, known := mask.New(), mask.New().WellKnown()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if !slices.Contains(names, name) {
				t.Fatalf("no pattern %s", name)
			}
			if got := known.String(c.text); got != c.want {
				t.Errorf("String = %q, want %q", got, c.want)
			}
			if got := plain.String(c.text); got != c.text {
				t.Errorf("without the patterns, String = %q", got)
			}
			for _, n := range c.near {
				if got := known.String(n); got != n {
					t.Errorf("the near miss %q = %q", n, got)
				}
			}
		})
	}
}

// TestUnitMaskPrivateKeys masks each line of a private key block in PEM
// or PGP armour from its BEGIN line to its END line, whether the block is
// masked whole or one line at a time, and leaves public blocks and the
// lines after a truncated key visible.
func TestUnitMaskPrivateKeys(t *testing.T) {
	m := mask.New().WellKnown()
	for _, c := range []struct{ name, text, want string }{
		{
			"RSA, with a header",
			"key:\n-----BEGIN RSA PRIVATE KEY-----\r\nProc-Type: 4,ENCRYPTED\n" +
				"MIIEowIBAAKCAQEA+/=\n-----END RSA PRIVATE KEY-----\nafter",
			"key:\n*****\r\n*****\n*****\n*****\nafter",
		},
		{
			"indented PKCS #8",
			"  -----BEGIN PRIVATE KEY-----\n  MIIBVQIBADANBgkq\n  -----END PRIVATE KEY-----  \n",
			"  *****\n  *****\n  *****  \n",
		},
		{
			"PGP, with a blank line",
			"-----BEGIN PGP PRIVATE KEY BLOCK-----\nVersion: GnuPG v2\n\nlQOYBF4\n=abcd\n" +
				"-----END PGP PRIVATE KEY BLOCK-----",
			"*****\n*****\n\n*****\n*****\n*****",
		},
		{
			"on one line, as JSON escapes it",
			`{"key": "-----BEGIN EC PRIVATE KEY-----\nMHcCAQEE\n-----END EC PRIVATE KEY-----\n"}`,
			`{"key": "*****\n"}`,
		},
		{
			"two on one line",
			"a -----BEGIN PRIVATE KEY-----x-----END PRIVATE KEY----- b " +
				"-----BEGIN PRIVATE KEY-----y-----END PRIVATE KEY----- c",
			"a ***** b ***** c",
		},
		{
			"truncated",
			"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\nApply complete!",
			"*****\n*****\nApply complete!",
		},
		{
			"public",
			"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n-----BEGIN PUBLIC KEY-----",
			"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n-----BEGIN PUBLIC KEY-----",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := m.String(c.text); got != c.want {
				t.Errorf("String = %q, want %q", got, c.want)
			}
			l := m.Lines()
			var got []string
			for line := range strings.SplitSeq(c.text, "\n") {
				got = append(got, string(l.Mask([]byte(line))))
			}
			if strings.Join(got, "\n") != c.want {
				t.Errorf("Lines = %q, want %q", got, c.want)
			}
			if got := mask.New().String(c.text); got != c.text {
				t.Errorf("without the patterns, String = %q", got)
			}
		})
	}
}

// TestUnitMaskAddMask adds words to a Masker, which leaves the one it
// grew unchanged, and to a stream of lines part way through it.
func TestUnitMaskAddMask(t *testing.T) {
	m := secrets("hunter22")
	n := m.With([]byte("swordfish"), []byte("ab"), []byte("two\nlines"))
	if got := n.String("hunter22 swordfish ab two lines"); got != "***** ***** ab two *****" {
		t.Errorf("grown = %q", got)
	}
	if got := m.String("hunter22 swordfish"); got != "***** swordfish" {
		t.Errorf("the Masker grown from = %q", got)
	}
	if m.With([]byte("ab"), nil) != m {
		t.Error("With of nothing to mask made a new Masker")
	}
	jwt := sample("eyJ", "hbGciOiJIUzI1NiJ9.eyJ", "zdWIiOiIxMjM0In0.c2ln")
	if got := m.WellKnown().
		With([]byte("swordfish")).
		String("swordfish " + jwt); got != "***** *****" {
		t.Errorf("grown from a well-known Masker = %q", got)
	}

	l := m.WellKnown().Lines()
	for _, c := range []struct {
		add        []string
		line, want string
	}{
		{nil, "swordfish 1", "swordfish 1"},
		{[]string{"swordfish", "on"}, "swordfish on 2", "***** on 2"},
		{nil, "-----BEGIN PRIVATE KEY-----", "*****"},
		{[]string{"marlin42"}, "MIIBVQIBADANBgkq", "*****"},
		{nil, "-----END PRIVATE KEY----- marlin42 hunter22", "***** ***** *****"},
	} {
		for _, w := range c.add {
			l.Add([]byte(w))
		}
		if got := l.Mask([]byte(c.line)); string(got) != c.want {
			t.Errorf("Mask(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

// TestUnitMaskWriterWellKnown carries a private key block across the
// Writer's writes.
func TestUnitMaskWriterWellKnown(t *testing.T) {
	var out bytes.Buffer
	w := mask.New().WellKnown().Writer(&out)
	for _, p := range []string{"-----BEGIN PRIVATE", " KEY-----\nMIIB", "VQIB\n-----END PRIVATE KEY-----\nok"} {
		if _, err := w.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "*****\n*****\n*****\nok" {
		t.Errorf("got %q", got)
	}
}

// FuzzMaskLines checks that masking text whole and masking it one line at
// a time through Lines agree, with the patterns applied.
func FuzzMaskLines(f *testing.F) {
	for _, seed := range []struct{ secret, text string }{
		{"hunter22", "a hunter22\n-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----"},
		{"MIIB", "-----BEGIN RSA PRIVATE KEY-----\n\n  MIIB  \nnot base64!\nMIIB"},
		{"", sample("ghp", "_", strings.Repeat("a", 36), " eyJa.eyJb.c AccountKey=", strings.Repeat("k", 24))},
	} {
		f.Add([]byte(seed.secret), []byte(seed.text))
	}
	f.Fuzz(func(t *testing.T, secret, text []byte) {
		m := mask.New(secret).WellKnown()
		want := m.Bytes(bytes.Clone(text))
		l := m.Lines()
		var got [][]byte
		for line := range bytes.SplitSeq(text, []byte("\n")) {
			got = append(got, bytes.Clone(l.Mask(line)))
		}
		if joined := bytes.Join(got, []byte("\n")); !bytes.Equal(joined, want) {
			t.Fatalf("Lines(%q) with %q = %q, Bytes = %q", text, secret, joined, want)
		}
	})
}
