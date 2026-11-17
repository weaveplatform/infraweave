package state

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// urlSteps are the steps of the signed state URLs: they keep the URL an
// answer names, and the object ID beside an upload URL, send it with no
// credential to a replica, and move time on for a URL or an upload.
type urlSteps struct {
	w       *harness.World
	urls    map[string]string
	objects map[string]string
}

func (u *urlSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the fixture "([^"]+)" is put with no credential at the URL "(\w+)" through ([AB])$`, u.fixturePut)
	sc.Step(`^the last answer is an encrypted envelope without "([^"]+)"$`, u.encryptedAnswer)
	sc.Step(`^the answer's url is kept as "(\w+)"$`, u.keep)
	sc.Step(`^'([^']*)' is put with no credential at the URL "(\w+)"( tampered)? through ([AB])$`,
		u.put)
	sc.Step(`^the upload of the URL "(\w+)" is one sealed object in the uploads store$`,
		u.uploaded)
	sc.Step(
		`^the URL "(\w+)" read with no credential through ([AB]) is the current state version `+
			`of the run "(\w+)", holding "([^"]+)"$`,
		u.current,
	)
	sc.Step(`^the URL "(\w+)" read with no credential through ([AB]) answers a (\d+) problem `+
		`"([a-z-]+)"$`, u.refused)
	sc.Step(`^the URL "(\w+)" read with no credential through ([AB]) is the current state `+
		`version of the stack "(\w+)"$`, u.stackCurrent)
	sc.Step(`^`+span+` pass(?:es)? for the URL "(\w+)"$`, u.age)
	sc.Step(`^`+span+` pass(?:es)? for the upload of the URL "(\w+)"$`, u.ageUpload)
}

// keep keeps the last answer's url as name, and its objectId when it
// names one.
func (u *urlSteps) keep(name string) error {
	var a struct {
		URL      string `json:"url"`
		ObjectID string `json:"objectId"`
	}
	last := u.w.Last()
	if json.Unmarshal(last.Body, &a) != nil || a.URL == "" {
		return fmt.Errorf("%w: no url in %d %s", harness.ErrStep, last.Status, last.Body)
	}
	if u.urls == nil {
		u.urls, u.objects = map[string]string{}, map[string]string{}
	}
	u.urls[name], u.objects[name] = a.URL, a.ObjectID
	return nil
}

// at is the URL kept as name on the replica's listener. Every URL is
// absolute under server.public_url, which no client of the scenario
// resolves; the path is the same on either replica.
func (u *urlSteps) at(name, replica string) (string, error) {
	url, ok := u.urls[name]
	if !ok {
		return "", fmt.Errorf("%w: no URL %q", harness.ErrStep, name)
	}
	path, ok := strings.CutPrefix(url, harness.PublicURL)
	if !ok {
		return "", fmt.Errorf("%w: %s is not under %s", harness.ErrStep, url, harness.PublicURL)
	}
	return u.w.Base(replica) + path, nil
}

// send sends a request with no Authorization header, as any client
// holding a signed URL does, and records the answer for the answer steps.
func (u *urlSteps) send(ctx context.Context, method, url, body string) (harness.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return harness.Response{}, fmt.Errorf("request: %w", err)
	}
	req.ContentLength = int64(len(body))
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return harness.Response{}, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		return harness.Response{}, fmt.Errorf("read %s %s: %w", method, url, err)
	}
	r := harness.Response{Status: resp.StatusCode, Header: resp.Header, Body: got}
	var p sdk.Problem
	if r.Status >= http.StatusBadRequest && json.Unmarshal(got, &p) == nil {
		r.Err = p
	}
	u.w.Sent = append(u.w.Sent, r)
	return r, nil
}

// put PUTs body to the URL kept as name, a character inside its token
// changed when tampered, leaving the answer to the answer steps.
func (u *urlSteps) put(ctx context.Context, body, name, tampered, replica string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	if tampered != "" {
		i, c := len(url)-10, "A"
		if url[i] == 'A' {
			c = "B"
		}
		url = url[:i] + c + url[i+1:]
	}
	_, err = u.send(ctx, http.MethodPut, url, body)
	return err
}

// uploaded checks that the upload URL kept as name stored one live
// object in the uploads store under its object ID, with a sealed content
// key.
func (u *urlSteps) uploaded(ctx context.Context, name string) error {
	object, ok := u.objects[name]
	if !ok || object == "" {
		return fmt.Errorf("%w: no object ID kept with %q", harness.ErrStep, name)
	}
	var n int
	if err := u.w.Row(ctx, `SELECT count(*) FROM blob_objects
		WHERE store = 'uploads' AND state = 'live' AND content_key_sealed IS NOT NULL
			AND split_part(key, '/', 3) = $1`, []any{object}, &n); err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: %d sealed objects of %s in uploads", harness.ErrStep, n, object)
	}
	return nil
}

// current checks that the URL kept as name serves, with no credential,
// the current version of the workspace the run kept as run wrote to, as
// its SHA-256 records it, and that the state holds want, unsealed.
func (u *urlSteps) current(ctx context.Context, name, replica, run, want string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	r, err := u.send(ctx, http.MethodGet, url, "")
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("%w: GET answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	id, ok := u.w.IDs[run]
	if !ok {
		return fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var sum []byte
	if err := u.w.Row(ctx, `SELECT v.sha256
		FROM state_workspaces s JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE (s.account_id, s.stack_id, s.name) IN (
			SELECT account_id, stack_id, workspace FROM managed_state_versions WHERE run_id = $1)`,
		[]any{harness.UUIDOf(id)}, &sum); err != nil {
		return err
	}
	if got := sha256.Sum256(r.Body); !bytes.Equal(got[:], sum) ||
		!bytes.Contains(r.Body, []byte(want)) {
		return fmt.Errorf("%w: the download's SHA-256 is %x, the current version's %x; "+
			"holding %q: %t", harness.ErrStep, got, sum, want, bytes.Contains(r.Body, []byte(want)))
	}
	return nil
}

// refused checks that the URL kept as name answers a GET with no
// credential with the problem of status and slug.
func (u *urlSteps) refused(ctx context.Context, name, replica, status, slug string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	r, err := u.send(ctx, http.MethodGet, url, "")
	if err != nil {
		return err
	}
	return harness.IsProblem(r, "the download", status, slug) //nolint:wrapcheck // a step error
}

// stackCurrent checks that the URL kept as name serves, with no
// credential, the current version of the default workspace of the home
// account's stack slug, as its SHA-256 records it.
func (u *urlSteps) stackCurrent(ctx context.Context, name, replica, slug string) error {
	url, err := u.at(name, replica)
	if err != nil {
		return err
	}
	r, err := u.send(ctx, http.MethodGet, url, "")
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("%w: GET answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	var sum []byte
	if err := u.w.Row(ctx, `SELECT v.sha256
		FROM stacks s
		JOIN state_workspaces w ON w.account_id = s.account_id AND w.stack_id = s.id AND w.name = ''
		JOIN managed_state_versions v ON v.account_id = w.account_id AND v.id = w.current_version_id
		WHERE s.account_id = $1 AND s.slug = $2`,
		[]any{harness.UUIDOf(harness.Home.Account), slug}, &sum); err != nil {
		return err
	}
	if got := sha256.Sum256(r.Body); !bytes.Equal(got[:], sum) {
		return fmt.Errorf("%w: the download's SHA-256 is %x, the current version's %x",
			harness.ErrStep, got, sum)
	}
	return nil
}

// tokenPurposes are the purposes the server derives each kind of state
// URL token's key for, by the kind's byte, which leads the token.
//
// ref: decision 0010 § "Upload, import and download URLs"
var tokenPurposes = map[byte]string{'U': "state-upload-url", 'D': "state-download-url"}

// tokenTail is a token's expiry, 8 bytes of Unix seconds, and the
// HMAC-SHA-256 after it, which end every token.
const tokenTail = 8 + sha256.Size

// age moves the expiry of the URL kept as name back by passing, and
// signs its token again as the server signs one: under the key its kind
// derives from the data key the token names, read from the scenario's
// keyring. A real server's clock cannot be moved, so the URL ages
// instead. A token is its kind, the data key's ID, the account, the
// subject, the target and the expiry, then the MAC of all of them, in
// unpadded base64url.
//
// ref: decision 0010 § "Upload, import and download URLs"
// ref: decision 0016
func (u *urlSteps) age(ctx context.Context, passing, name string) error {
	d, err := spanOf(passing)
	if err != nil {
		return err
	}
	url, ok := u.urls[name]
	if !ok {
		return fmt.Errorf("%w: no URL %q", harness.ErrStep, name)
	}
	cut := strings.LastIndexByte(url, '/') + 1
	b, err := base64.RawURLEncoding.Strict().DecodeString(url[cut:])
	if err != nil || len(b) < 1+4+tokenTail {
		return fmt.Errorf("%w: the URL %s carries no token", harness.ErrStep, url)
	}
	purpose, ok := tokenPurposes[b[0]]
	if !ok {
		return fmt.Errorf("%w: a token of kind %q", harness.ErrStep, b[0])
	}
	keys, pool, err := u.w.Keyring(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	key, err := keys.DerivedKey(purpose, binary.BigEndian.Uint32(b[1:5]))
	if err != nil {
		return fmt.Errorf("%w: the token's key: %w", harness.ErrStep, err)
	}
	body := b[:len(b)-sha256.Size]
	at := len(body) - 8
	expires := int64(binary.BigEndian.Uint64(body[at:])) - int64(d/time.Second) //nolint:gosec // two's complement
	binary.BigEndian.PutUint64(body[at:], uint64(expires))                      //nolint:gosec // two's complement
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	u.urls[name] = url[:cut] + base64.RawURLEncoding.EncodeToString(mac.Sum(body))
	return nil
}

// spanOf is the duration of a span as the steps name one, such as
// "15 minutes" or "1 day".
func spanOf(s string) (time.Duration, error) {
	n, unit, _ := strings.Cut(s, " ")
	count, err := strconv.Atoi(n)
	per, ok := map[string]time.Duration{
		"minute": time.Minute, "hour": time.Hour, "day": 24 * time.Hour,
	}[strings.TrimSuffix(unit, "s")]
	if err != nil || !ok {
		return 0, fmt.Errorf("%w: no span %q", harness.ErrStep, s)
	}
	return time.Duration(count) * per, nil
}

// ageUpload moves the creation and expiry of the upload the URL kept as
// name stored back by passing, a span PostgreSQL reads as an interval:
// the uploads store's retention, which blob.expire applies, then reads
// it as that much older.
//
// ref: decision 0010 § "Upload, import and download URLs"
func (u *urlSteps) ageUpload(ctx context.Context, passing, name string) error {
	object, ok := u.objects[name]
	if !ok || object == "" {
		return fmt.Errorf("%w: no object ID kept with %q", harness.ErrStep, name)
	}
	var n int
	if err := u.w.Row(ctx, `WITH b AS (
			UPDATE blob_objects SET created_at = created_at - $2::interval,
				expires_at = expires_at - $2::interval
			WHERE store = 'uploads' AND split_part(key, '/', 3) = $1
			RETURNING 1)
		SELECT count(*) FROM b`, []any{object, passing}, &n); err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: %d uploads of %s to age", harness.ErrStep, n, object)
	}
	return nil
}
