package acceptance

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// assigned checks that the last answer, a poll's, assigns the run kept as
// run, and keeps its run token as token, for a step to send with.
func (w *workerSteps) assigned(run, token string) error {
	var poll wire.PollResult
	r := w.o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &poll) != nil ||
		poll.Assignment == nil {
		return fmt.Errorf("%w: answered %d %s", errStep, r.status, r.body)
	}
	if poll.Assignment.Run != w.o.ids[run] || poll.Assignment.RunToken == "" {
		return fmt.Errorf("%w: assigned %+v, want the run %s", errStep, *poll.Assignment,
			w.o.ids[run])
	}
	w.o.a.tokens[token] = poll.Assignment.RunToken
	return nil
}

// spec is the last answer, a run's specification.
func (w *workerSteps) spec() (wire.RunSpec, error) {
	var s wire.RunSpec
	r := w.o.a.s.last()
	if r.status != http.StatusOK || json.Unmarshal(r.body, &s) != nil {
		return s, fmt.Errorf("%w: answered %d %s", errStep, r.status, r.body)
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
		return fmt.Errorf("%w: the variable %s is %+v", errStep, name, v)
	}
	return fmt.Errorf("%w: the specification has no variable %s", errStep, name)
}

// sealed opens the last answer's sealed part, a run's specification's,
// with the private key of the request kept as csr, for the run and state
// version the specification names, and checks that it holds the secret
// name's value want, an in-run API token and a 256-bit artifact key, and
// nothing the API's schema does not name.
func (w *workerSteps) sealed(csr, name, want string) error {
	s, err := w.spec()
	if err != nil {
		return err
	}
	key, ok := w.keys[csr].(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("%w: no P-256 key of %q", errStep, csr)
	}
	priv, err := key.ECDH()
	if err != nil {
		return err
	}
	plain, err := seal.OpenEnvelope(priv, s.Run.Id, s.Run.StateVersion, s.Sealed)
	if err != nil {
		return fmt.Errorf("%w: the sealed part does not open: %w", errStep, err)
	}
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	var secrets wire.RunSpecSecrets
	if err := dec.Decode(&secrets); err != nil {
		return fmt.Errorf("%w: the secrets: %w", errStep, err)
	}
	if secrets.Environment[name] != want || secrets.ApiToken == "" ||
		len(secrets.ArtifactKey) != 32 {
		return fmt.Errorf("%w: %s is %q, with %d bytes of artifact key", errStep, name,
			secrets.Environment[name], len(secrets.ArtifactKey))
	}
	return nil
}
