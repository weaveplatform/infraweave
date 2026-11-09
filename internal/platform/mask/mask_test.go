package mask_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

func secrets(values ...string) *mask.Masker {
	b := make([][]byte, len(values))
	for i, v := range values {
		b[i] = []byte(v)
	}
	return mask.New(b...)
}

func TestUnitMask(t *testing.T) {
	for _, tc := range []struct {
		name    string
		secrets []string
		text    string
		want    string
	}{
		{"no secrets", nil, "token=hunter22", "token=hunter22"},
		{"one occurrence", []string{"hunter22"}, "token=hunter22", "token=*****"},
		{"every occurrence", []string{"hunter22"}, "a hunter22 b hunter22", "a ***** b *****"},
		{"every secret", []string{"hunter22", "swordfish"}, "hunter22 swordfish", "***** *****"},
		{"no match", []string{"hunter22"}, "hunter2 hunter2", "hunter2 hunter2"},
		{"the same secret twice", []string{"hunter22", "hunter22"}, "hunter22", "*****"},
		{"UTF-8", []string{"pässwörd"}, "x pässwörd y pässwör", "x ***** y pässwör"},
		{"a multi-byte rune", []string{"🔑🔑"}, "key 🔑🔑 key", "key ***** key"},
		{
			"file content, line by line",
			[]string{"line-one\nline-two\r\nok\n"},
			"line-one, line-two and ok", "*****, ***** and ok",
		},
		{"a file's lines apart", []string{"line-one\nline-two"}, "line-one\nline-two", "*****\n*****"},
		{"a file's lines joined", []string{"line-one\nline-two"}, "line-oneline-two", "*****"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := secrets(tc.secrets...)
			if got := m.String(tc.text); got != tc.want {
				t.Errorf("String = %q, want %q", got, tc.want)
			}
			if got := m.Bytes([]byte(tc.text)); string(got) != tc.want {
				t.Errorf("Bytes = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnitMaskOverlaps checks that occurrences which overlap or touch join
// one run, masked once, so neither their count nor their length shows.
func TestUnitMaskOverlaps(t *testing.T) {
	for _, tc := range []struct {
		name    string
		secrets []string
		text    string
		want    string
	}{
		{"a secret overlapping itself", []string{"abab"}, "x ababab x", "x ***** x"},
		{"two secrets overlapping", []string{"abcd", "cdef"}, "x abcdef x", "x ***** x"},
		{"a secret inside another", []string{"secret", "ecre"}, "secret ecre", "***** *****"},
		{"a secret ending another", []string{"secret", "cret"}, "secret", "*****"},
		{"adjacent occurrences", []string{"abcd"}, "abcdabcd abcd", "***** *****"},
		{"adjacent secrets", []string{"abcd", "wxyz"}, "abcdwxyz", "*****"},
		{
			"a long secret that absorbs two earlier runs",
			[]string{"ab12", "x9yy", "12-x9yy-zz"},
			"QQab12-x9yy-zzQQ", "QQ*****QQ",
		},
		{"runs one byte apart", []string{"abcd"}, "abcd-abcd", "*****-*****"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := secrets(tc.secrets...).String(tc.text); got != tc.want {
				t.Errorf("String = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnitMaskMinimum checks the 4-byte rule: a shorter secret, or a
// shorter line of one, stays visible.
func TestUnitMaskMinimum(t *testing.T) {
	m := secrets("", "on", "abc", "abcd", "yes\nlonger")
	if got, want := m.String("on abc abcd yes longer"), "on abc ***** yes *****"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
	if got := secrets("on", "abc").String("on abc"); got != "on abc" {
		t.Errorf("only short secrets = %q", got)
	}
}

func TestUnitMaskBytesAliasing(t *testing.T) {
	m := secrets("hunter22")
	clean := []byte("nothing here")
	if got := m.Bytes(clean); &got[0] != &clean[0] {
		t.Error("Bytes copied text with nothing to mask")
	}
	dirty := []byte("pw hunter22")
	if got := m.Bytes(dirty); string(got) != "pw *****" || string(dirty) != "pw hunter22" {
		t.Errorf("Bytes = %q and changed its input to %q", got, dirty)
	}
}

func TestUnitMaskWriter(t *testing.T) {
	m := secrets("hunter22")
	write := func(t *testing.T, parts ...string) string {
		t.Helper()
		var out bytes.Buffer
		w := m.Writer(&out)
		for _, p := range parts {
			if n, err := w.Write([]byte(p)); err != nil || n != len(p) {
				t.Fatalf("Write(%q) = %d, %v", p, n, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	long := strings.Repeat("a", mask.MaxLine)
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"whole lines", []string{"pw hunter22\nok\n"}, "pw *****\nok\n"},
		{"a secret split across writes", []string{"pw hun", "te", "r22 and\nnext"}, "pw ***** and\nnext"},
		{"the last line, unterminated", []string{"hunter22"}, "*****"},
		{"nothing", nil, ""},
		{"empty lines", []string{"\n\n", "\n"}, "\n\n\n"},
		{"a line of MaxLine bytes", []string{long + "\n"}, long + "\n"},
		{"a line cut at MaxLine", []string{long + "bbbb\nhunter22\n"}, long + "\n*****\n"},
		{"a line cut across writes", []string{long[:10], long[10:] + "bb", "bb\nhunter22"}, long + "\n*****"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := write(t, tc.parts...); got != tc.want {
				if len(got) > 80 {
					t.Errorf("got %d bytes, want %d", len(got), len(tc.want))
					return
				}
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("byte by byte", func(t *testing.T) {
		text := "a hunter22 b\nhunter22hunter22\n"
		parts := make([]string, len(text))
		for i := range text {
			parts[i] = text[i : i+1]
		}
		if got := write(t, parts...); got != "a ***** b\n*****\n" {
			t.Errorf("got %q", got)
		}
	})
}

func TestUnitMaskWriterClose(t *testing.T) {
	var out bytes.Buffer
	w := secrets("hunter22").Writer(&out)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("a second Close = %v", err)
	}
	if n, err := w.Write([]byte("x")); n != 0 || !errors.Is(err, mask.ErrClosed) {
		t.Errorf("Write after Close = %d, %v", n, err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q", out.String())
	}
}

type failing struct{ calls int }

var errBroken = errors.New("broken pipe")

func (f *failing) Write(p []byte) (int, error) {
	f.calls++
	return 0, errBroken
}

func TestUnitMaskWriterErrors(t *testing.T) {
	f := &failing{}
	w := secrets("hunter22").Writer(f)
	if n, err := w.Write([]byte("one\ntwo\nthree")); !errors.Is(err, errBroken) || n != 4 {
		t.Errorf("Write = %d, %v; want 4 and the wrapped writer's error", n, err)
	}
	if n, err := w.Write([]byte("\n")); !errors.Is(err, errBroken) || n != 0 {
		t.Errorf("a later Write = %d, %v", n, err)
	}
	if err := w.Close(); !errors.Is(err, errBroken) {
		t.Errorf("Close = %v", err)
	}
	if err := w.Close(); !errors.Is(err, errBroken) {
		t.Errorf("a second Close = %v", err)
	}
	if f.calls != 1 {
		t.Errorf("the wrapped writer was called %d times after failing", f.calls)
	}

	f = &failing{}
	w = secrets("hunter22").Writer(f)
	if _, err := w.Write([]byte("unterminated")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); !errors.Is(err, errBroken) {
		t.Errorf("Close of a pending line = %v", err)
	}
}
