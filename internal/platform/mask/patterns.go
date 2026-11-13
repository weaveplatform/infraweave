package mask

import (
	"bytes"
	"regexp"
	"strings"
)

// pattern is the shape of one kind of well-known secret. Its regular
// expression's first group is the part masked, so a name that only
// introduces the value, such as AccountKey=, stays visible. hints are
// literals of which every match holds one, so a line that holds none
// costs a few substring searches rather than a regular expression's.
type pattern struct {
	name  string
	re    *regexp.Regexp
	hints [][]byte
}

// token is a pattern whose secret, secret, is made of the bytes the
// bracket expression class names: a match next to one of them is part of
// a longer word, not the secret, and stays visible.
func token(name, class, secret string, hints ...string) pattern {
	return pattern{
		name:  name,
		re:    regexp.MustCompile(`(?:^|[^` + class + `])(` + secret + `)(?:$|[^` + class + `])`),
		hints: literals(hints),
	}
}

func literals(hints []string) [][]byte {
	out := make([][]byte, len(hints))
	for i, h := range hints {
		out[i] = []byte(h)
	}
	return out
}

// wellKnown are the patterns of the well-known secrets Spacelift's stack
// setting masks, but the private key blocks, which span lines and
// keyBegin finds.
//
// ref: decision 0013 § "Masking"
// ref: report 13 §4.4
var wellKnown = []pattern{
	token("aws-access-key-id", `A-Za-z0-9`,
		`(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16}`,
		"A3T", "AKIA", "ASIA", "ABIA", "ACCA"),
	token("entra-id-client-secret", `A-Za-z0-9_~.\-`,
		`[A-Za-z0-9_~.]{3}[0-9]Q~[A-Za-z0-9_~.\-]{31,34}`, "Q~"),
	{
		name: "azure-connection-string",
		re: regexp.MustCompile(
			`(?:AccountKey|SharedAccessKey|SharedAccessSignature)=([^;\s'"]{20,})`,
		),
		hints: literals([]string{
			"AccountKey=", "SharedAccessKey=", "SharedAccessSignature=",
		}),
	},
	token("github-pat-classic", `A-Za-z0-9_`, `ghp_[A-Za-z0-9]{36,}`, "ghp_"),
	token("github-pat-fine-grained", `A-Za-z0-9_`, `github_pat_[A-Za-z0-9_]{82,}`, "github_pat_"),
	token("github-app-token", `A-Za-z0-9_`, `gh[su]_[A-Za-z0-9]{36,}`, "ghs_", "ghu_"),
	token("github-refresh-token", `A-Za-z0-9_`, `ghr_[A-Za-z0-9]{36,}`, "ghr_"),
	token("github-oauth-token", `A-Za-z0-9_`, `gho_[A-Za-z0-9]{36,}`, "gho_"),
	token("jwt", `A-Za-z0-9_\-`,
		`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]*`, "eyJ"),
	token("slack-token", `A-Za-z0-9\-`, `(?:xox[abeoprs]|xapp)-[A-Za-z0-9\-]{10,}`,
		"xox", "xapp-"),
}

// A private key's PEM or PGP armour, such as -----BEGIN RSA PRIVATE
// KEY----- or -----BEGIN PGP PRIVATE KEY BLOCK-----, opens a block that
// the matching END line closes.
var (
	keyBegin = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)
	keyEnd   = regexp.MustCompile(`-----END (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)
	// keyHeader is a PEM or armour header inside a block, such as
	// Proc-Type: 4,ENCRYPTED or Version: GnuPG v2.
	keyHeader = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*: `)
)

// keyMarker is the literal every BEGIN and END line holds.
var keyMarker = []byte("-----")

// patternSpans finds the well-known secrets in line, which holds no
// newline, as spans offset by at. key is whether a private key block an
// earlier line opened is open at the line's start; patternSpans answers
// whether one is open at its end.
func patternSpans(line []byte, at int, key bool) ([]span, bool) {
	var out []span
	for _, p := range wellKnown {
		if !hinted(line, p.hints) {
			continue
		}
		for from := 0; from < len(line); {
			m := p.re.FindSubmatchIndex(line[from:])
			if m == nil {
				break
			}
			out = append(out, span{at + from + m[2], at + from + m[3]})
			// The next search starts after the secret, not after the
			// byte that bounded it, which may bound the next one too.
			from += m[3]
		}
	}
	found, key := keySpans(line, key)
	for _, s := range found {
		out = append(out, span{at + s.start, at + s.end})
	}
	return out, key
}

func hinted(line []byte, hints [][]byte) bool {
	for _, h := range hints {
		if bytes.Contains(line, h) {
			return true
		}
	}
	return false
}

// keySpans finds the private key blocks in line, given whether one is
// open at its start, and answers whether one is open at its end. Every
// line of an open block is masked from its first byte that is not a
// space to its last, but the BEGIN line from its marker and the END line
// to its marker. A line that is neither the END line nor the body of a key,
// base64 or a header, closes a block whose END line never came, so a
// truncated key does not mask the rest of the text.
func keySpans(line []byte, key bool) ([]span, bool) {
	var out []span
	from := 0
	if key {
		switch end := keyEnd.FindIndex(line); {
		case end != nil:
			out = append(out, span{trimmed(line).start, end[1]})
			from = end[1]
		case body(line):
			if s := trimmed(line); s.end > s.start {
				out = append(out, s)
			}
			return out, true
		}
	}
	if !bytes.Contains(line[from:], keyMarker) {
		return out, false
	}
	for {
		begin := keyBegin.FindIndex(line[from:])
		if begin == nil {
			return out, false
		}
		start := from + begin[0]
		from += begin[1]
		end := keyEnd.FindIndex(line[from:])
		if end == nil {
			return append(out, span{start, trimmed(line).end}), true
		}
		from += end[1]
		out = append(out, span{start, from})
	}
}

// body reports whether line is a line inside a private key block: base64,
// a header, or empty, as armour's blank line after its headers is.
func body(line []byte) bool {
	s := trimmed(line)
	text := string(line[s.start:s.end])
	if keyHeader.MatchString(text) {
		return true
	}
	return strings.Trim(text,
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=") == ""
}

// trimmed is the span of line without its leading and trailing spaces,
// tabs and carriage returns.
func trimmed(line []byte) span {
	start, end := 0, len(line)
	for start < end && isSpace(line[start]) {
		start++
	}
	for end > start && isSpace(line[end-1]) {
		end--
	}
	return span{start, end}
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' }
