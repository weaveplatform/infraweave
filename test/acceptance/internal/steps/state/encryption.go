package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (b *backendSteps) registerEncryption(sc *godog.ScenarioContext) {
	sc.Step(`^the tool environment of the run "(\w+)" sets "([A-Z_]+)" from fixture "([^"]+)"$`, b.fixtureEnv)
	sc.Step(`^the tool environment of the run "(\w+)" unsets "([A-Z_]+)"$`, b.unsetEnv)
	sc.Step(`^the fixture "([^"]+)" is posted to the state of the run "(\w+)" with (its run token|the (?:home|member) key) through ([AB])$`, b.postFixture)
	sc.Step(`^the current state version of the run "(\w+)" is client-encrypted$`, b.encrypted)
	sc.Step(`^the version of the run "(\w+)" has the next serial and the same lineage as the run "(\w+)"$`, b.advanced)
	sc.Step(`^the last standard error of the run "(\w+)" holds '([^']*)'$`, b.errorHolds)
}

func fixtureBody(path string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(harness.RepoRoot, path))
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", path, err)
	}
	return b, nil
}

// fixtureEnv supplies the tool's encryption configuration without putting
// its passphrase into the repository the scenario checks out.
func (b *backendSteps) fixtureEnv(run, name, path string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	value, err := fixtureBody(path)
	if err != nil {
		return err
	}
	tr.env = append(tr.env, name+"="+string(value))
	return nil
}

func (b *backendSteps) unsetEnv(run, name string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	tr.env = slices.DeleteFunc(tr.env, func(value string) bool {
		return strings.HasPrefix(value, name+"=")
	})
	return nil
}

// errorHolds compares a diagnostic independently of terminal colours,
// borders and wrapping, while keeping stdout available as a state body.
func (b *backendSteps) errorHolds(run, want string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	out := colours.ReplaceAllString(tr.stderr, "")
	out = strings.Join(strings.Fields(strings.ReplaceAll(out, "│", "")), " ")
	if !strings.Contains(out, want) {
		return fmt.Errorf("%w: the standard error is %q", harness.ErrStep, out)
	}
	return nil
}

// postFixture sends the vendor's actual encrypted envelope with the
// selected credential, leaving the refusal for the problem steps.
func (b *backendSteps) postFixture(ctx context.Context, path, run, credential, replica string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	body, err := fixtureBody(path)
	if err != nil {
		return err
	}
	user, pass := "run", b.w.Tokens[tr.token]
	if credential != "its run token" {
		user = "api"
		if pass, err = b.password(credential); err != nil {
			return err
		}
	}
	_, err = b.stateBody(ctx, http.MethodPost, tr.path, user, pass, string(body), replica)
	return err
}

// encrypted checks the server's metadata as well as the current run's
// decrypted state, whose serial and lineage current already verifies.
func (b *backendSteps) encrypted(ctx context.Context, run string) error {
	if err := b.current(ctx, run); err != nil {
		return err
	}
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	var (
		encrypted bool
		serial    *int64
		lineage   *string
	)
	if err := b.w.Row(ctx, `SELECT v.client_encrypted, v.serial, v.lineage
		FROM state_workspaces s JOIN managed_state_versions v
			ON v.account_id = s.account_id AND v.id = s.current_version_id
		WHERE s.stack_id = (SELECT stack_id FROM runs WHERE id = $1) AND s.name = ''`,
		[]any{harness.UUIDOf(tr.id)}, &encrypted, &serial, &lineage); err != nil {
		return err
	}
	if !encrypted || serial == nil || lineage == nil {
		return fmt.Errorf("%w: client_encrypted=%t, serial=%v, lineage=%v", harness.ErrStep, encrypted, serial, lineage)
	}
	if err := b.passed(ctx, http.MethodPost, "", run, http.StatusOK); err != nil {
		return err
	}
	fmt.Printf("acceptance: relay POST %s answered 200; client_encrypted=true serial=%d lineage=%s\n", tr.path, *serial, *lineage)
	return nil
}

// advanced compares two real applies' state pull outputs, independently
// of the stored metadata checked by current.
func (b *backendSteps) advanced(run, previous string) error {
	var states [2]struct {
		Serial  *int64 `json:"serial"`
		Lineage string `json:"lineage"`
	}
	for n, name := range []string{run, previous} {
		tr, err := b.run(name)
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(tr.stdout), &states[n]) != nil || states[n].Serial == nil {
			return fmt.Errorf("%w: the output of %s is no state", harness.ErrStep, name)
		}
	}
	if *states[0].Serial != *states[1].Serial+1 || states[0].Lineage == "" || states[0].Lineage != states[1].Lineage {
		return fmt.Errorf("%w: the state did not advance one serial in the same lineage: %+v", harness.ErrStep, states)
	}
	return nil
}

// fixturePut uploads recorded vendor bytes, allowing the same denial
// scenario to exercise plain and encrypted state without inventing one.
func (u *urlSteps) fixturePut(ctx context.Context, path, name, replica string) error {
	body, err := fixtureBody(path)
	if err != nil {
		return err
	}
	return u.put(ctx, string(body), name, "", replica)
}

// downloadPut imports the encrypted bytes the download serves, rather
// than state pull's decrypted view of the same state.
func (i *importSteps) downloadPut(ctx context.Context, download, upload, replica string) error {
	from, err := i.urls.at(download, replica)
	if err != nil {
		return err
	}
	r, err := i.urls.send(ctx, http.MethodGet, from, "")
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("%w: the download answered %d", harness.ErrStep, r.Status)
	}
	to, err := i.urls.at(upload, replica)
	if err != nil {
		return err
	}
	_, err = i.urls.send(ctx, http.MethodPut, to, string(r.Body))
	return err
}

// encryptedAnswer validates the downloaded body as an envelope without
// exposing its ciphertext, and checks that the plaintext is absent.
func (u *urlSteps) encryptedAnswer(secret string) error {
	var body map[string]json.RawMessage
	r := u.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &body) != nil ||
		len(body["encrypted_data"]) == 0 || len(body["encryption_version"]) == 0 ||
		strings.Contains(string(r.Body), secret) {
		return fmt.Errorf("%w: the downloaded state is not an encrypted envelope without the plaintext", harness.ErrStep)
	}
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	fmt.Printf("acceptance: encrypted state's top-level keys: %s\n", strings.Join(keys, ", "))
	return nil
}
