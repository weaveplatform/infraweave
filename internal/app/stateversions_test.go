package app

import (
	"bytes"
	"crypto/fips140"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	fsdriver "github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	stateassembly "github.com/weaveplatform/infraweave/internal/state/assembly"
)

// stateMax bounds the versions of the state tests' servers, past one
// part of every driver, so a large state streams in parts.
const stateMax = 8 << 20

// stateServer is the API over the claim world's domains with a state
// domain of its own, whose versions go to a bucket of driver: fs under
// root, or the driver's emulator. store reads the bucket's bytes as
// stored.
type stateServer struct {
	h     http.Handler
	state *stateassembly.Domain
	store objectstore.Store
	root  string
}

func newStateServer(t *testing.T, w *claimWorld, driver string) stateServer {
	t.Helper()
	c := objectstore.Defaults()
	c.Driver = driver
	var (
		b    objectstore.Bucket
		root string
	)
	switch driver {
	case objectstore.DriverS3:
		b = objectstoretest.S3(t, "states")
	case objectstore.DriverGCS:
		b = objectstoretest.GCS(t, "states")
	case objectstore.DriverAzureBlob:
		b = objectstoretest.AzureBlob(t, "states")
	default:
		root = t.TempDir()
		c.FS.Root, c.FS.URL = root, "http://infraweave.test"
		f, err := fsdriver.New(c.FS, w.keys)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		b = f
	}
	store, err := objectstore.New(c, func(string) (objectstore.Bucket, error) { return b, nil })
	if err != nil {
		t.Fatal(err)
	}
	st := stateassembly.New(stateassembly.Deps{
		DB:       w.db.Primary(),
		Stacks:   w.d.Stacks.Stacks(),
		Access:   w.d.Runs.StateAccess(),
		Objects:  blobs.New(w.q, queue.New(w.q), objectstore.WriterOnly(store), w.keys, c),
		Jobs:     queue.New(w.q),
		Settings: stateassembly.Settings{MaxSize: stateMax},
	})
	d := w.d
	d.State = st
	sc := config.Defaults().Server
	return stateServer{
		h:     API(discard(), sc, memory(sc), d, Platform{}, w.blobs),
		state: st,
		store: store,
		root:  root,
	}
}

// applying is a run of the claim world's stack one, claimed and moved to
// APPLYING, where it may take the exclusive lock and write; its token is
// returned, and the stack keeps its state in infraweave.
func applying(t *testing.T, w *claimWorld) string {
	t.Helper()
	pool := w.pool(t, w.acct, nil)
	id := w.run(t, w.acct, pool,
		runSpec{kernel.RunTypeTracked, kernel.RunPriorityNormal, false}, time.Now())
	p := w.poll(t.Context(), 0, w.worker(t, pool, false), false, 5)
	if claimed(t, p) != id {
		t.Fatalf("the claim took %+v, want %s", p.a, id)
	}
	w.exec(t, `UPDATE runs SET state = 'APPLYING' WHERE id = $1`, id)
	w.exec(t, `UPDATE stacks SET manages_state = true WHERE id = $1`, w.stacks["one"])
	return p.a.RunToken
}

// write sends size bytes of body to target as Terraform's http backend
// writes a state, with md5 as its Content-MD5 when it is set.
func (c stateCalls) write(
	t *testing.T,
	target string,
	body io.Reader,
	size int64,
	md5 string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, body)
	req.ContentLength = size
	req.SetBasicAuth("run", c.token)
	if md5 != "" {
		req.Header.Set("Content-MD5", md5)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// post writes body whole, with md5 as its Content-MD5.
func (c stateCalls) post(t *testing.T, target, body, md5 string) *httptest.ResponseRecorder {
	t.Helper()
	return c.write(t, target, strings.NewReader(body), int64(len(body)), md5)
}

// contentMD5 is body's Content-MD5 as Terraform sends it, or none under
// fips140=only, where Go refuses MD5 and the server checks none.
func contentMD5(body string) string {
	if fips140.Enforced() {
		return ""
	}
	sum := md5.Sum([]byte(body))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// versionsOf counts the versions of the stack one's workspace and the
// states store's objects under its key prefix in each state.
func versionsOf(t *testing.T, w *claimWorld, workspace string) (int, map[string]int) {
	t.Helper()
	ctx := t.Context()
	var n int
	if err := w.q.QueryRow(ctx, `SELECT count(*) FROM managed_state_versions
		WHERE account_id = $1 AND stack_id = $2 AND workspace = $3`,
		w.acct, w.stacks["one"], workspace).Scan(&n); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("%s/%s/%x/", w.acct, w.stacks["one"], sha256.Sum256([]byte(workspace)))
	rows, err := w.q.Query(ctx, `SELECT state, count(*) FROM blob_objects
		WHERE store = 'states' AND starts_with(key, $1) GROUP BY state`, prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	objects := map[string]int{}
	for rows.Next() {
		var (
			state string
			count int
		)
		if err := rows.Scan(&state, &count); err != nil {
			t.Fatal(err)
		}
		objects[state] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return n, objects
}

// current is the object key, SHA-256 and serial of the version the stack
// one's workspace points at.
func current(t *testing.T, w *claimWorld, workspace string) (string, []byte, *int64) {
	t.Helper()
	var (
		key    string
		sum    []byte
		serial *int64
	)
	if err := w.q.QueryRow(t.Context(), `SELECT v.object_key, v.sha256, v.serial
		FROM state_workspaces s JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE s.account_id = $1 AND s.stack_id = $2 AND s.name = $3`,
		w.acct, w.stacks["one"], workspace).Scan(&key, &sum, &serial); err != nil {
		t.Fatal(err)
	}
	return key, sum, serial
}

// secretState is a plain state of serial whose sensitive output password
// is secret.
func secretState(serial int, secret string) string {
	return fmt.Sprintf(`{"version":4,"terraform_version":"1.9.0","serial":%d,`+
		`"lineage":"5e1d","outputs":{"password":{"value":%q,"type":"string",`+
		`"sensitive":true}},"resources":[]}`, serial, secret)
}

// TestAccStateVersions writes and reads state versions through the API
// on every object store driver, over PostgreSQL: a write must name the
// exclusive lock its run holds; a version's object is live, sealed and
// current; the same body again adds no version and its object goes; a
// mismatched Content-MD5 is refused unless fips140=only, where it is
// stored with the body's SHA-256; a client-encrypted body is stored
// unparsed; a state past a driver's part streams both ways; a body over
// state.max_size is refused before it streams; one cut short leaves only
// a pending object, for blob.expire; and a write after the run releases
// its lock answers 423 with {}.
func TestAccStateVersions(t *testing.T) {
	w := newClaimWorld(t)
	token := applying(t, w)
	for _, driver := range []string{
		objectstore.DriverFS, objectstore.DriverS3, objectstore.DriverGCS,
		objectstore.DriverAzureBlob,
	} {
		t.Run(driver, func(t *testing.T) {
			s := newStateServer(t, w, driver)
			c := stateCalls{h: s.h, token: token}
			ws := stateapi.BaseURL + "/stacks/" + w.stacks["one"].String() + "/workspaces/" +
				driver
			id := "lock-" + driver
			if rec := c.send(t, http.MethodGet, ws, ""); rec.Code != http.StatusNoContent {
				t.Fatalf("a read of no state = %d %s", rec.Code, rec.Body)
			}
			if rec := c.send(t, stateapi.MethodLock, ws, lockInfo(id)); rec.Code != http.StatusOK {
				t.Fatalf("LOCK = %d %s", rec.Code, rec.Body)
			}
			secret := "s3cr3t-" + kernel.NewULID().String()
			state := secretState(1, secret)
			if rec := c.post(t, ws, state, ""); rec.Code != http.StatusLocked ||
				rec.Body.String() != lockInfo(id) {
				t.Errorf("a write naming no lock = %d %s", rec.Code, rec.Body)
			}

			under := ws + "?ID=" + id
			if rec := c.post(t, under, state, contentMD5(state)); rec.Code != http.StatusOK {
				t.Fatalf("a write = %d %s", rec.Code, rec.Body)
			}
			key, sum, serial := current(t, w, driver)
			if want := sha256.Sum256([]byte(state)); !bytes.Equal(sum, want[:]) || serial == nil ||
				*serial != 1 {
				t.Errorf("the current version's SHA-256 %x, serial %v", sum, serial)
			}
			if n, objects := versionsOf(t, w, driver); n != 1 || objects["live"] != 1 {
				t.Errorf("%d versions, objects %v", n, objects)
			}
			r, err := s.store.Get(t.Context(), objectstore.States, key)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := io.ReadAll(r)
			r.Close()
			if err != nil || bytes.Contains(stored, []byte(secret)) ||
				bytes.Contains(stored, []byte("5e1d")) {
				t.Errorf("the stored object holds the state as written: %v", err)
			}
			rec := c.send(t, http.MethodGet, ws, "")
			if rec.Code != http.StatusOK || rec.Body.String() != state ||
				rec.Header().Get("Content-Type") != "application/json" ||
				rec.Header().Get("Content-MD5") != "" {
				t.Errorf("a read = %d %v %s", rec.Code, rec.Header(), rec.Body)
			}

			if rec := c.post(t, under, state, contentMD5(state)); rec.Code != http.StatusOK {
				t.Fatalf("the same write again = %d %s", rec.Code, rec.Body)
			}
			if n, objects := versionsOf(t, w, driver); n != 1 || objects["deleting"] != 1 {
				t.Errorf("the same body again: %d versions, objects %v", n, objects)
			}

			mismatched := secretState(2, secret)
			rec = c.post(t, under, mismatched, "AAAAAAAAAAAAAAAAAAAAAA==")
			n, objects := versionsOf(t, w, driver)
			if fips140.Enforced() {
				_, sum, _ := current(t, w, driver)
				want := sha256.Sum256([]byte(mismatched))
				if rec.Code != http.StatusOK || n != 2 || !bytes.Equal(sum, want[:]) {
					t.Errorf("an unchecked Content-MD5 = %d, %d versions, SHA-256 %x", rec.Code,
						n, sum)
				}
			} else {
				wantProblem(t, rec, http.StatusBadRequest, "Content-MD5")
				if n != 1 || objects["deleting"] != 2 {
					t.Errorf("a mismatched Content-MD5: %d versions, objects %v", n, objects)
				}
			}

			encrypted := `{"meta":{"key_provider.pbkdf2.k":"e30="},"encrypted_data":"` +
				base64.StdEncoding.EncodeToString([]byte(secret)) + `","encryption_version":"v0"}`
			if rec := c.post(
				t,
				under,
				encrypted,
				contentMD5(encrypted),
			); rec.Code != http.StatusOK {
				t.Fatalf("a client-encrypted write = %d %s", rec.Code, rec.Body)
			}
			if _, _, serial := current(t, w, driver); serial != nil {
				t.Errorf("a client-encrypted version has serial %d", *serial)
			}

			large := `{"version":4,"serial":3,"lineage":"5e1d","resources":[` +
				strings.Repeat(`{"type":"null_resource","name":"n"},`, (6<<20)/36) + `{}]}`
			if rec := c.post(t, under, large, contentMD5(large)); rec.Code != http.StatusOK {
				t.Fatalf("a large write = %d %s", rec.Code, rec.Body)
			}
			rec = c.send(t, http.MethodGet, ws, "")
			if rec.Code != http.StatusOK || rec.Body.String() != large {
				t.Errorf("a large read = %d, %d bytes of %d", rec.Code, rec.Body.Len(), len(large))
			}

			before, objects := versionsOf(t, w, driver)
			wantProblem(t, c.write(t, under, strings.NewReader("{}"), stateMax+1, ""),
				http.StatusRequestEntityTooLarge, "")
			rec = c.write(t, under, io.MultiReader(strings.NewReader(`{"version":4,`),
				iotest.ErrReader(io.ErrUnexpectedEOF)), 100, "")
			wantProblem(t, rec, http.StatusBadRequest, "")
			if n, after := versionsOf(t, w, driver); n != before ||
				after["pending"] != objects["pending"]+1 || after["live"] != objects["live"] {
				t.Errorf("an oversized and a cut write left %d versions, objects %v, before %v",
					n, after, objects)
			}

			if rec := c.send(
				t,
				stateapi.MethodUnlock,
				ws,
				lockInfo(id),
			); rec.Code != http.StatusOK {
				t.Fatalf("UNLOCK = %d %s", rec.Code, rec.Body)
			}
			if rec := c.post(t, under, state, ""); rec.Code != http.StatusLocked ||
				rec.Body.String() != `{}` {
				t.Errorf("a write after the unlock = %d %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestAccStateSealed writes a state with a sensitive output to a stack's
// default workspace on the fs driver and reads every byte under the
// store's root: none holds the secret, while the API reads the state
// back and Outputs answers the output, marked sensitive. A
// client-encrypted version then has no outputs.
func TestAccStateSealed(t *testing.T) {
	w := newClaimWorld(t)
	c := stateCalls{token: applying(t, w)}
	s := newStateServer(t, w, objectstore.DriverFS)
	c.h = s.h
	stack := stateapi.BaseURL + "/stacks/" + w.stacks["one"].String()
	if rec := c.send(t, stateapi.MethodLock, stack, lockInfo("l")); rec.Code != http.StatusOK {
		t.Fatalf("LOCK = %d %s", rec.Code, rec.Body)
	}
	secret := "s3cr3t-" + kernel.NewULID().String()
	state := secretState(1, secret)
	if rec := c.post(t, stack+"?ID=l", state, contentMD5(state)); rec.Code != http.StatusOK {
		t.Fatalf("a write = %d %s", rec.Code, rec.Body)
	}
	files := 0
	if err := filepath.WalkDir(s.root, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		files++
		b, err := os.ReadFile(path)
		if err == nil && bytes.Contains(b, []byte(secret)) {
			t.Errorf("%s holds the secret", path)
		}
		return err
	}); err != nil || files == 0 {
		t.Fatalf("read %d files: %v", files, err)
	}
	if rec := c.send(t, http.MethodGet, stack, ""); rec.Body.String() != state {
		t.Errorf("a read = %d %s", rec.Code, rec.Body)
	}
	out, err := s.state.Versions().Outputs(t.Context(), w.acct, w.stacks["one"])
	if err != nil || len(out) != 1 || out[0].Name != "password" || !out[0].Sensitive ||
		string(out[0].Value) != fmt.Sprintf("%q", secret) {
		t.Errorf("Outputs %+v, %v", out, err)
	}
	latest, ok, err := s.state.Versions().Latest(t.Context(), w.acct, w.stacks["one"],
		stateapi.DefaultWorkspace)
	if err != nil || !ok || latest.Size != int64(len(state)) || latest.ClientEncrypted {
		t.Errorf("Latest %+v, %v, %v", latest, ok, err)
	}
	encrypted := `{"meta":{},"encrypted_data":"eA==","encryption_version":"v0"}`
	if rec := c.post(t, stack+"?ID=l", encrypted, ""); rec.Code != http.StatusOK {
		t.Fatalf("a client-encrypted write = %d %s", rec.Code, rec.Body)
	}
	if out, err := s.state.Versions().Outputs(t.Context(), w.acct, w.stacks["one"]); out != nil ||
		err != nil {
		t.Errorf("Outputs of a client-encrypted state %+v, %v", out, err)
	}
}
