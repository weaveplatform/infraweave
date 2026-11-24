package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/cloud/awsfake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// issued are the AssumeRole calls that issued credentials to the run kept
// as run, in the order AWS STS answered them: those whose session name
// starts with the run's ID, which the session name of a run's phase does.
func (s *stsSteps) issued(run string) ([]awsfake.Call, error) {
	if s.fake == nil {
		return nil, fmt.Errorf("%w: no AWS STS in this scenario", harness.ErrStep)
	}
	id, ok := s.w.IDs[run]
	if !ok {
		return nil, fmt.Errorf("%w: no run %q", harness.ErrStep, run)
	}
	var out []awsfake.Call
	for _, c := range s.fake.Calls() {
		if c.Action == "AssumeRole" && c.Issued.AccessKeyID != "" && strings.HasPrefix(c.SessionName, id+"@") {
			out = append(out, c)
		}
	}
	return out, nil
}

// externalIDs checks that AWS STS issued credentials to the run kept as
// run once for each of want's external IDs, comma-separated and in order,
// and for nothing else: once per handover, from the side its phase reads.
func (s *stsSteps) externalIDs(run, want string) error {
	calls, err := s.issued(run)
	if err != nil {
		return err
	}
	got := make([]string, len(calls))
	for n, c := range calls {
		got[n] = c.ExternalID
	}
	if wanted := splitList(s.w.Resolve(want)); !slices.Equal(got, wanted) {
		return fmt.Errorf("%w: the run %s was issued credentials with the external IDs %q, want %q",
			harness.ErrStep, run, got, wanted)
	}
	return nil
}

// assumption checks the one call that issued credentials to the run kept
// as run with the external ID id: it assumed role with the session name,
// duration and session tags given, the tags as key=value, comma-separated
// and sorted by key.
func (s *stsSteps) assumption(run, role, id, session string, seconds int, tags string) error {
	c, err := s.call(run, id)
	if err != nil {
		return err
	}
	keys := slices.Sorted(maps.Keys(c.Tags))
	got := make([]string, len(keys))
	for n, k := range keys {
		got[n] = k + "=" + c.Tags[k]
	}
	if c.RoleARN != role || c.SessionName != s.w.Resolve(session) ||
		c.Duration != time.Duration(seconds)*time.Second || strings.Join(got, ",") != s.w.Resolve(tags) {
		return fmt.Errorf("%w: assumed %s as %q for %s with the tags %q", harness.ErrStep, c.RoleARN,
			c.SessionName, c.Duration, got)
	}
	return nil
}

// call is the one call that issued credentials to the run kept as run with
// the external ID id.
func (s *stsSteps) call(run, id string) (awsfake.Call, error) {
	calls, err := s.issued(run)
	if err != nil {
		return awsfake.Call{}, err
	}
	id = s.w.Resolve(id)
	var found []awsfake.Call
	for _, c := range calls {
		if c.ExternalID == id {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return awsfake.Call{}, fmt.Errorf("%w: %d calls issued the run %s credentials with the external ID %s",
			harness.ErrStep, len(found), run, id)
	}
	return found[0], nil
}

// digests keeps as name the line a hook prints of the credentials AWS STS
// issued to the run kept as run with the external ID id: "aws:" and the
// SHA-256 of AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SECURITY_TOKEN
// and AWS_SESSION_TOKEN, in that order, so a log step finds them without
// the scenario ever naming a credential.
func (s *stsSteps) digests(run, id, name string) error {
	c, err := s.call(run, id)
	if err != nil {
		return err
	}
	line := "aws:"
	for _, v := range []string{c.Issued.AccessKeyID, c.Issued.SecretAccessKey, c.Issued.SessionToken, c.Issued.SessionToken} {
		sum := sha256.Sum256([]byte(v))
		line += " " + hex.EncodeToString(sum[:])
	}
	s.w.IDs[name] = line
	return nil
}

// nowhere checks that no credential AWS STS issued in the scenario, its
// access key, secret key or session token, is in any of at least n objects
// of the scenario's bucket, which keeps the runs' logs, artifacts and
// saved workspaces, in any answer of the scenario or in a server's log.
func (s *stsSteps) nowhere(ctx context.Context, n int) error {
	if s.fake == nil {
		return fmt.Errorf("%w: no AWS STS in this scenario", harness.ErrStep)
	}
	store, err := s.w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names @objectstore
	}
	checked := 0
	for _, c := range s.fake.Calls() {
		for _, v := range []string{c.Issued.AccessKeyID, c.Issued.SecretAccessKey, c.Issued.SessionToken} {
			if v == "" {
				continue
			}
			checked++
			keys, read, err := store.Containing(ctx, s.w.Bucket, []byte(v))
			switch {
			case err != nil:
				return err //nolint:wrapcheck // names the bucket
			case len(keys) > 0:
				return fmt.Errorf("%w: the objects %q hold an issued credential", harness.ErrStep, keys)
			case read < n:
				return fmt.Errorf("%w: the bucket holds %d objects, want at least %d", harness.ErrStep, read, n)
			}
			for i, r := range s.w.Sent {
				if strings.Contains(string(r.Body), v) {
					return fmt.Errorf("%w: answer %d holds an issued credential", harness.ErrStep, i+1)
				}
			}
			for i, srv := range s.w.Servers {
				if strings.Contains(string(srv.Stdout()), v) {
					return fmt.Errorf("%w: server %d logged an issued credential", harness.ErrStep, i+1)
				}
			}
		}
	}
	if checked == 0 {
		return fmt.Errorf("%w: AWS STS issued no credential", harness.ErrStep)
	}
	return nil
}

// computed checks that the last answer, a stack, lists each of names,
// comma-separated, in its runtimeConfig as a computed write-only
// environment variable with no value.
func (s *stsSteps) computed(names string) error {
	var v struct {
		RuntimeConfig []struct {
			Element struct {
				ID        string  `json:"id"`
				Runtime   bool    `json:"runtime"`
				WriteOnly bool    `json:"writeOnly"`
				Value     *string `json:"value"`
			} `json:"element"`
		} `json:"runtimeConfig"`
	}
	r := s.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &v) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	var got []string
	for _, e := range v.RuntimeConfig {
		if e.Element.Runtime && e.Element.WriteOnly && e.Element.Value == nil {
			got = append(got, e.Element.ID)
		}
	}
	for _, name := range splitList(names) {
		if !slices.Contains(got, name) {
			return fmt.Errorf("%w: the computed write-only variables are %q, want %s among them",
				harness.ErrStep, got, name)
		}
	}
	return nil
}

// refuse makes role refuse every assumption, as a role whose trust policy
// no longer names the installation does.
func (s *stsSteps) refuse(role string) error {
	if s.fake == nil {
		return fmt.Errorf("%w: no AWS STS in this scenario", harness.ErrStep)
	}
	s.fake.AddRole(awsfake.Role{ARN: role})
	return nil
}
