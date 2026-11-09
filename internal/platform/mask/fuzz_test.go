package mask_test

import (
	"bytes"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// reference masks text by brute force: it marks every byte of every
// occurrence of every secret line, then replaces each run of marked bytes.
func reference(secrets [][]byte, text []byte) []byte {
	marked := make([]bool, len(text))
	for _, s := range secrets {
		for line := range bytes.SplitSeq(s, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if len(line) < mask.MinLen {
				continue
			}
			for i := 0; i+len(line) <= len(text); i++ {
				if bytes.HasPrefix(text[i:], line) {
					for j := range line {
						marked[i+j] = true
					}
				}
			}
		}
	}
	var out []byte
	for i := 0; i < len(text); {
		if !marked[i] {
			out = append(out, text[i])
			i++
			continue
		}
		out = append(out, mask.Replacement...)
		for i < len(text) && marked[i] {
			i++
		}
	}
	return out
}

// FuzzMask checks String, Bytes and the Writer against reference, so no
// byte of a secret's occurrence stays visible. Its seeds run in every test
// run.
func FuzzMask(f *testing.F) {
	for _, seed := range []struct {
		a, b, text string
		split      uint
	}{
		{"hunter22", "", "token=hunter22\n", 9},
		{"abab", "baba", "abababab babab", 3},
		{"abcd", "cdef", "abcdef abcd cdef", 5},
		{"secret", "ecre", "secret ecre secre", 2},
		{"line-one\nline-two\r\nok", "on", "line-one\nline-two ok on", 12},
		{"pässwörd", "🔑🔑🔑", "pässwörd 🔑🔑🔑 pässw", 6},
		{"aaaa", "aaaaa", "aaaaaaaaa", 4},
		{"ab12", "12-x9yy-zz", "QQab12-x9yy-zzQQ", 7},
		{"****", "", "a **** b", 1},
	} {
		f.Add([]byte(seed.a), []byte(seed.b), []byte(seed.text), seed.split)
	}
	f.Fuzz(func(t *testing.T, a, b, text []byte, split uint) {
		for line := range bytes.SplitSeq(text, []byte("\n")) {
			if len(line) > mask.MaxLine {
				t.Skip("a line the Writer cuts")
			}
		}
		m := mask.New(a, b)
		want := reference([][]byte{a, b}, text)
		if got := m.Bytes(bytes.Clone(text)); !bytes.Equal(got, want) {
			t.Fatalf("Bytes(%q) with %q and %q = %q, want %q", text, a, b, got, want)
		}
		if got := m.String(string(text)); got != string(want) {
			t.Fatalf("String(%q) with %q and %q = %q, want %q", text, a, b, got, want)
		}
		var out bytes.Buffer
		w := m.Writer(&out)
		at := 0
		if len(text) > 0 {
			at = int(split % uint(len(text)+1))
		}
		for _, part := range [][]byte{text[:at], text[at:]} {
			if _, err := w.Write(part); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("Writer(%q | %q) with %q and %q = %q, want %q",
				text[:at], text[at:], a, b, out.Bytes(), want)
		}
	})
}
