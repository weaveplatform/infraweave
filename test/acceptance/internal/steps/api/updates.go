package acceptance

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// updateSteps drive conditional writes: they keep the ETags answers carry,
// by name, and send a write whose If-Match names one.
type updateSteps struct {
	o *orgSteps
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
	r := u.o.a.s.last()
	tag := r.header.Get("ETag")
	if tag == "" {
		return fmt.Errorf("%w: no ETag in %d %s", errStep, r.status, r.body)
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
		return fmt.Errorf("%w: no ETag kept as %q", errStep, tag)
	}
	r, err := u.o.a.do(ctx, replica, token, method, u.o.resolve(path), body(u.o.resolve(raw)),
		sdk.Header{Name: "If-Match", Value: etag})
	if err != nil && r.status == 0 {
		return err
	}
	return nil
}
