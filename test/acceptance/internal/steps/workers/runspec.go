package workers

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// assigned checks that the last answer, a poll's, assigns the run kept as
// run, and keeps its run token as token, for a step to send with.
func (w *workerSteps) assigned(run, token string) error {
	var poll wire.PollResult
	r := w.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &poll) != nil ||
		poll.Assignment == nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	if poll.Assignment.Run != w.w.IDs[run] || poll.Assignment.RunToken == "" {
		return fmt.Errorf("%w: assigned %+v, want the run %s", harness.ErrStep, *poll.Assignment,
			w.w.IDs[run])
	}
	w.w.Tokens[token] = poll.Assignment.RunToken
	return nil
}

// spec is the last answer, a run's specification.
func (w *workerSteps) spec() (wire.RunSpec, error) {
	var s wire.RunSpec
	r := w.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &s) != nil {
		return s, fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	return s, nil
}

// variable checks that the last answer, a run's specification, carries
// the variable name with value want in clear, or, when want is empty,
// write-only with no value.
func (w *workerSteps) variable(name, want string) error {
	s, err := w.spec()
	if err != nil {
		return err
	}
	for _, v := range s.Environment {
		if v.Name != name {
			continue
		}
		switch {
		case want == "" && v.WriteOnly && v.Value == nil:
			return nil
		case want != "" && !v.WriteOnly && v.Value != nil && *v.Value == want:
			return nil
		}
		return fmt.Errorf("%w: the variable %s is %+v", harness.ErrStep, name, v)
	}
	return fmt.Errorf("%w: the specification has no variable %s", harness.ErrStep, name)
}

// sealed opens the last answer's sealed part, a run's specification's,
// with the private key of the request kept as csr, and checks that it
// holds the secret name's value want, an in-run API token and a 256-bit
// artifact key.
func (w *workerSteps) sealed(csr, name, want string) error {
	secrets, err := w.open(csr)
	if err != nil {
		return err
	}
	if secrets.Environment[name] != want || secrets.ApiToken == "" ||
		len(secrets.ArtifactKey) != 32 {
		return fmt.Errorf("%w: %s is %q, with %d bytes of artifact key", harness.ErrStep, name,
			secrets.Environment[name], len(secrets.ArtifactKey))
	}
	return nil
}

// inRun keeps the in-run API token of the last answer's sealed part, a
// run's specification's opened with the private key of the request kept
// as csr, as the token name.
func (w *workerSteps) inRun(csr, name string) error {
	secrets, err := w.open(csr)
	if err != nil {
		return err
	}
	if secrets.ApiToken == "" {
		return fmt.Errorf("%w: the sealed part holds no in-run API token", harness.ErrStep)
	}
	w.w.Tokens[name] = secrets.ApiToken
	return nil
}

// open opens the last answer's sealed part, a run's specification's, with
// the private key of the request kept as csr, for the run and state
// version the specification names, refusing anything the API's schema
// does not name.
func (w *workerSteps) open(csr string) (wire.RunSpecSecrets, error) {
	var secrets wire.RunSpecSecrets
	s, err := w.spec()
	if err != nil {
		return secrets, err
	}
	key, ok := w.w.Keys[csr].(*ecdsa.PrivateKey)
	if !ok {
		return secrets, fmt.Errorf("%w: no P-256 key of %q", harness.ErrStep, csr)
	}
	priv, err := key.ECDH()
	if err != nil {
		return secrets, err
	}
	plain, err := seal.OpenEnvelope(priv, s.Run.Id, s.Run.StateVersion, s.Sealed)
	if err != nil {
		return secrets, fmt.Errorf("%w: the sealed part does not open: %w", harness.ErrStep, err)
	}
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&secrets); err != nil {
		return secrets, fmt.Errorf("%w: the secrets: %w", harness.ErrStep, err)
	}
	return secrets, nil
}
