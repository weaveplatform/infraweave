package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// stalling refuses its first calls of each kind with errs, and every
// call with always when it is set; it keeps the calls in order.
type stalling struct {
	errs         map[string][]error
	always       error
	calls        []string
	declarations []wire.UploadURLsRequest
	recordings   []wire.ArtifactsRequest
}

func (s *stalling) next(call string) error {
	s.calls = append(s.calls, call)
	if s.always != nil {
		return s.always
	}
	if errs := s.errs[call]; len(errs) > 0 {
		s.errs[call] = errs[1:]
		return errs[0]
	}
	return nil
}

func (s *stalling) UploadURLs(_ context.Context, _, _ string, in wire.UploadURLsRequest) ([]wire.SignedUpload, error) {
	s.declarations = append(s.declarations, in)
	if err := s.next("urls"); err != nil {
		return nil, err
	}
	out := make([]wire.SignedUpload, len(in.Uploads))
	for i, u := range in.Uploads {
		out[i] = wire.SignedUpload{Kind: u.Kind, Name: u.Name}
	}
	return out, nil
}

func (s *stalling) PutFile(context.Context, wire.SignedUpload, string) error {
	return s.next("put")
}

func (s *stalling) Artifacts(_ context.Context, _ string, _ string, in wire.ArtifactsRequest) error {
	s.recordings = append(s.recordings, in)
	return s.next("artifacts")
}

// saving is a proposed run whose plan's JSON is in its temporary
// directory and whose uploads go to u.
func saving(t *testing.T, u Uploads) *run {
	t.Helper()
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planJSON, []byte(`{"format_version":"1.2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &run{
		h: handoff.Handoff{
			Run: "01HZX4Y9Q2W8E5R7T3V6K1M0PA", RunToken: "token",
			Spec: wire.RunSpec{Run: wire.RunSpecRun{
				Account: "01HZX4Y9Q2W8E5R7T3V6K1M0PB", Type: wire.PROPOSED,
			}},
			Secrets: wire.RunSpecSecrets{ArtifactKey: bytes.Repeat([]byte{7}, 32)},
		},
		d:       Deps{Uploads: u, Log: slog.New(slog.DiscardHandler)},
		temp:    dir,
		options: vendors.Options{PlanJSON: planJSON},
	}
}

// TestUnitSaveRetries checks that a declaration, a PUT and a record the
// worker API or the store cannot take for now are taken again after 1, 2
// and 4 seconds, a record alone once the PUTs were taken; that uploads
// still refused when the next wait would pass ten minutes fail the save;
// and that a cancelled context ends the wait.
func TestUnitSaveRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &stalling{errs: map[string][]error{
			"urls": {&client.Error{Status: 503}}, "put": {errLost},
			"artifacts": {&client.Error{Status: 429}},
		}}
		began := time.Now()
		if note := saving(t, s).save(t.Context(), 5, plan.Plan{}); note != "" ||
			time.Since(began) != 7*time.Second {
			t.Errorf("after 3 refusals: %q after %s", note, time.Since(began))
		}
		if want := []string{
			"urls", "urls", "put", "urls", "put", "put", "artifacts", "artifacts",
		}; !slices.Equal(s.calls, want) {
			t.Errorf("calls %q, want %q", s.calls, want)
		}

		s = &stalling{always: &client.Error{Op: "upload-urls", Status: 502}}
		began = time.Now()
		if note := saving(t, s).save(t.Context(), 5, plan.Plan{}); !strings.HasPrefix(note,
			"The plan's results could not be uploaded: ") ||
			time.Since(began) > reportRetry || time.Since(began) < reportRetry-reportWaitMax {
			t.Errorf("always refused: %q after %s", note, time.Since(began))
		}

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			<-time.After(90 * time.Second)
			cancel()
		}()
		s = &stalling{always: errLost}
		if note := saving(t, s).save(ctx, 5, plan.Plan{}); !strings.Contains(note,
			"upload the artifacts: context canceled") {
			t.Errorf("cancelled: %q", note)
		}
	})
}

// TestUnitSaveSignedRefusal checks that a PUT whose signature the store
// refuses is declared again, which signs it afresh, and sent after 1
// second; that the worker API's own 403 fails the save at once; and that
// a store refusing the signature until the next wait would pass ten
// minutes fails it with the store's answer.
func TestUnitSaveSignedRefusal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		skewed := client.Signed(&client.Error{Op: "upload plan.json", Status: 403})
		s := &stalling{errs: map[string][]error{"put": {skewed}}}
		began := time.Now()
		if note := saving(t, s).save(t.Context(), 5, plan.Plan{}); note != "" ||
			time.Since(began) != time.Second {
			t.Errorf("a refused signature: %q after %s", note, time.Since(began))
		}
		if want := []string{"urls", "put", "urls", "put", "put", "artifacts"}; !slices.Equal(
			s.calls, want) {
			t.Errorf("calls %q, want %q", s.calls, want)
		}

		s = &stalling{errs: map[string][]error{
			"urls": {&client.Error{Op: "declare uploads", Status: 403}},
		}}
		began = time.Now()
		if note := saving(t, s).save(t.Context(), 5, plan.Plan{}); note !=
			"The plan's results could not be uploaded: declare uploads: 403 Forbidden." ||
			time.Since(began) != 0 {
			t.Errorf("the worker API's 403: %q after %s", note, time.Since(began))
		}

		s = &stalling{always: client.Signed(&client.Error{Op: "upload workspace", Status: 400})}
		began = time.Now()
		if note := saving(t, s).save(t.Context(), 5, plan.Plan{}); note !=
			"The plan's results could not be uploaded: upload workspace: 400 Bad Request." ||
			time.Since(began) > reportRetry || time.Since(began) < reportRetry-reportWaitMax {
			t.Errorf("a signature always refused: %q after %s", note, time.Since(began))
		}
	})
}

// TestUnitSaveNotes checks the note of a save that cannot seal: a run ID
// that is no ULID, a plan's JSON that is gone, an artifact key that is no
// AES-256 key, and a temporary directory that is gone.
func TestUnitSaveNotes(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(r *run)
		want string
	}{
		"a run ID that is no ULID": {
			edit: func(r *run) { r.h.Run = "run-1" },
			want: "The run's ID is not a ULID: ",
		},
		"no plan JSON": {
			edit: func(r *run) { r.options.PlanJSON = filepath.Join(r.temp, "gone.json") },
			want: "The plan's plan.json could not be sealed: ",
		},
		"a short artifact key": {
			edit: func(r *run) { r.h.Secrets.ArtifactKey = []byte{7} },
			want: "The plan's plan.json could not be sealed: ",
		},
		"no temporary directory": {
			edit: func(r *run) { r.temp = filepath.Join(r.temp, "gone") },
			want: "The plan's plan.json could not be sealed: ",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := saving(t, &stalling{})
			c.edit(r)
			if note := r.save(t.Context(), 5, plan.Plan{}); !strings.HasPrefix(note, c.want) {
				t.Errorf("note %q, want %q", note, c.want)
			}
		})
	}
}

// Saving transforms only the server-readable copy. Every mode is sealed and
// leaves the raw plan untouched; malformed input and unknown modes upload none.
func TestUnitSaveSanitization(t *testing.T) {
	const raw = `{"format_version":"1.2","variables":{"secret":{"value":"private-642"},"visible":{"value":"plain-642"}},"configuration":{"root_module":{"variables":{"secret":{"sensitive":true}}}}}`
	for _, mode := range []string{"", "DEFAULT", "SMART", "NONE", "UNKNOWN"} {
		t.Run(mode, func(t *testing.T) {
			up := &stalling{}
			r := saving(t, up)
			if mode != "" {
				m := wire.PlanSanitization(mode)
				r.h.Spec.PlanSanitization = &m
			}
			if err := os.WriteFile(r.options.PlanJSON, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			note := r.save(t.Context(), 5, plan.Plan{})
			if mode == "UNKNOWN" {
				if !strings.Contains(note, sanitize.ErrMode.Error()) || len(up.calls) != 0 {
					t.Fatalf("unknown mode did not fail before upload: %q, %v", note, up.calls)
				}
				return
			}
			if note != "" {
				t.Fatal(note)
			}
			declared, recorded := slices.Clone(up.declarations[0].Uploads), slices.Clone(up.recordings[0].Artifacts)
			if len(declared) != 2 || len(recorded) != 2 {
				t.Fatal("missing uploads")
			}
			receipt, recordReceipt := declared[0].PlanSanitization, recorded[0].PlanSanitization
			if mode == "" {
				rawDecl, _ := json.Marshal(up.declarations[0])
				rawRecord, _ := json.Marshal(up.recordings[0])
				if receipt != nil || recordReceipt != nil || bytes.Contains(rawDecl, []byte("planSanitization")) || bytes.Contains(rawRecord, []byte("planSanitization")) {
					t.Fatal("old server received a new receipt field")
				}
			} else if receipt == nil || recordReceipt == nil || string(*receipt) != mode || string(*recordReceipt) != mode {
				t.Fatal("mode receipt missing or changed")
			}
			if declared[1].PlanSanitization != nil || recorded[1].PlanSanitization != nil {
				t.Fatal("receipt added to changes document")
			}
			stored, err := os.ReadFile(filepath.Join(r.temp, PlanArtifact+".sealed"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(stored, []byte("private-642")) {
				t.Fatal("unsealed secret")
			}
			keys, note := r.objectKeys("PLANNING", 5)
			if note != "" {
				t.Fatal(note)
			}
			key, err := seal.ArtifactKey(r.h.Secrets.ArtifactKey, keys(PlanArtifact))
			if err != nil {
				t.Fatal(err)
			}
			reader, err := seal.NewReader(bytes.NewReader(stored), key)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(plain) || bytes.Contains(plain, []byte("private-642")) != (mode == "NONE") ||
				bytes.Contains(plain, []byte("plain-642")) != (mode == "NONE" || mode == "SMART") {
				t.Fatalf("mode %s has incorrect value preservation", mode)
			}
			source, err := os.ReadFile(r.options.PlanJSON)
			if err != nil || string(source) != raw {
				t.Fatal("raw plan changed")
			}
		})
	}
	for _, raw := range []string{`{"format_version":"1.2","variables":{"x":1}}`, `{}`} {
		up := &stalling{}
		r := saving(t, up)
		if err := os.WriteFile(r.options.PlanJSON, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if note := r.save(t.Context(), 5, plan.Plan{}); !strings.Contains(note, "sanitize plan JSON") || len(up.calls) != 0 {
			t.Fatalf("malformed document uploaded: %q", note)
		}
	}
}
