package api

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// updateSteps drive conditional writes: they keep the ETags answers carry,
// by name, and send a write whose If-Match names one.
type updateSteps struct {
	w *harness.World
	// etags are the kept entity tags by name.
	etags map[string]string
}

func (u *updateSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer's ETag is kept as "(\w+)"$`, u.keep)
	sc.Step(
		`^token "(\w+)" sends "([A-Z]+) ([^"]+)"(?: with '([^']*)')? if matching the ETag "(\w+)" through ([AB])$`,
		u.send,
	)
}

// keep names the last answer's ETag.
func (u *updateSteps) keep(name string) error {
	r := u.w.Last()
	tag := r.Header.Get("ETag")
	if tag == "" {
		return fmt.Errorf("%w: no ETag in %d %s", harness.ErrStep, r.Status, r.Body)
	}
	u.etags[name] = tag
	return nil
}

// send sends the request with the named token and an If-Match of the kept
// ETag tag.
func (u *updateSteps) send(
	ctx context.Context,
	token, method, path, raw, tag, replica string,
) error {
	etag, ok := u.etags[tag]
	if !ok {
		return fmt.Errorf("%w: no ETag kept as %q", harness.ErrStep, tag)
	}
	r, err := u.w.Do(ctx, replica, token, method, u.w.Resolve(path), harness.Body(u.w.Resolve(raw)),
		sdk.Header{Name: "If-Match", Value: etag})
	if err != nil && r.Status == 0 {
		return err
	}
	return nil
}
