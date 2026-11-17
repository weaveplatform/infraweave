package handover_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
)

func TestUnitHandoverMountedBytes(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty": {}, "text": []byte("zulu\n"), "base64 text": []byte("enVsdQ=="),
		"binary": {0, 0xff, 'A', '\n', 0},
	} {
		for _, writeOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/writeOnly=%t", name, writeOnly), func(t *testing.T) {
				w := newWorld(t)
				saved := base64.StdEncoding.EncodeToString(raw)
				sum := sha256.Sum256([]byte(saved))
				checksum, mode := hex.EncodeToString(sum[:]), "600"
				w.env.resolved.Elements = []configapi.Element{{
					Name: "mounted.bin", FileMount: true, WriteOnly: writeOnly,
					Value: secret(saved), Checksum: checksum, FileMode: &mode,
				}}
				h, err := w.handover(t, w.run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(h.Spec.Files) != 1 {
					t.Fatalf("files = %+v", h.Spec.Files)
				}
				file := h.Spec.Files[0]
				if file.Path != "mounted.bin" || file.WriteOnly != writeOnly ||
					file.Checksum != checksum || file.Mode == nil || *file.Mode != mode {
					t.Errorf("file metadata = %+v", file)
				}
				var opened sealed
				if err := json.Unmarshal(h.Secrets.Reveal(), &opened); err != nil {
					t.Fatal(err)
				}
				content := file.Content
				if writeOnly {
					if content != nil {
						t.Error("write-only file content entered the public specification")
					}
					var exists bool
					content, exists = opened.Files[file.Path]
					if !exists {
						t.Error("write-only file is absent from the sealed document")
					}
				} else if len(opened.Files) != 0 {
					t.Error("a readable file entered the sealed document")
				}
				if !bytes.Equal(content, raw) {
					t.Errorf("mounted bytes = %x, want %x", content, raw)
				}
				if string(w.env.resolved.Elements[0].Value.Reveal()) != saved {
					t.Error("the saved API value changed during delivery")
				}
			})
		}
	}
}

func TestUnitHandoverInvalidMountedEncoding(t *testing.T) {
	for _, writeOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("writeOnly=%t", writeOnly), func(t *testing.T) {
			w := newWorld(t)
			const saved = "invalid-secret!"
			w.env.resolved.Elements = []configapi.Element{{
				Name: "mounted.bin", FileMount: true, WriteOnly: writeOnly, Value: secret(saved),
			}}
			_, err := w.handover(t, w.run.ID)
			var corrupt base64.CorruptInputError
			if !errors.As(err, &corrupt) || !strings.Contains(err.Error(), "mounted.bin") ||
				strings.Contains(err.Error(), saved) {
				t.Errorf("invalid mounted content error = %v", err)
			}
		})
	}
}
