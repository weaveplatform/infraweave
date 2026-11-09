package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Allower decides whether the request ctx carries may use a feature,
// refusing it with a *core.Refusal; core.Service is one.
type Allower interface {
	Allow(ctx context.Context, feature string) error
}

// Middleware refuses a request whose operation's capability, or an option
// its body sets, the Allower refuses: 403 feature-disabled or feature-not-in-tier.
// gate resolves the request's Gate; a request it resolves none for, such
// as a 404, passes.
func Middleware(
	a Allower,
	gate func(*nethttp.Request) (featuresapi.Gate, bool),
) func(nethttp.Handler) nethttp.Handler {
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			g, ok := gate(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			needs, err := needed(r, g)
			switch {
			case errors.Is(err, errTooLarge):
				problem.Write(w, problem.PayloadTooLarge.New(fmt.Sprintf(
					"A request body may carry at most %d bytes.", MaxBody)))
				return
			case err != nil:
				problem.Write(w, problem.InvalidRequest.New("The request body could not be read."))
				return
			}
			for _, f := range needs {
				err := a.Allow(r.Context(), f)
				if refused, ok := errors.AsType[*core.Refusal](err); ok {
					problem.Write(w, refusal(refused))
					return
				}
				if err != nil {
					problem.Failure(w, fmt.Errorf("features: %w", err))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBody is the largest body an option-gated operation's request may
// carry. It is held in memory to find the options it sets, so without a
// bound one request could exhaust memory; it equals idempotency.MaxBody, so
// a body one middleware admits the other admits too.
const MaxBody = 1 << 20

var errTooLarge = errors.New("the body is over MaxBody")

// needed is the features r needs: its operation's capability, then each
// option whose value its body sets, where a gate with no value is set by
// any value but null. The body is read and restored, and one that is not
// JSON sets no option; the strict handler refuses it.
func needed(r *nethttp.Request, g featuresapi.Gate) ([]string, error) {
	needs := []string{g.Feature}
	if len(g.Options) == 0 || r.Body == nil {
		return needs, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read the body: %w", err)
	}
	if len(body) > MaxBody {
		return nil, errTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var doc any
	if json.Unmarshal(body, &doc) != nil {
		return needs, nil
	}
	for _, o := range g.Options {
		if v := valueAt(doc, o.Pointer); v != nil && (o.Value == "" || fmt.Sprint(v) == o.Value) {
			needs = append(needs, o.Feature)
		}
	}
	return needs, nil
}

// pointerUnescape undoes a JSON Pointer reference token's escapes.
var pointerUnescape = strings.NewReplacer("~1", "/", "~0", "~")

// valueAt is the value of doc at pointer through its objects' members, or
// nil when a member on the way is absent or not an object.
func valueAt(doc any, pointer string) any {
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		object, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = object[pointerUnescape.Replace(token)]
	}
	return doc
}

func refusal(r *core.Refusal) problem.Problem {
	if r.Disabled {
		p := problem.FeatureDisabled.New(fmt.Sprintf(
			"The operator has switched off %s for this installation.", r.Feature))
		p.Feature = r.Feature
		return p
	}
	p := problem.FeatureNotInTier.New(fmt.Sprintf(
		"The organization's tier does not include %s.", r.Feature))
	p.Feature, p.Tiers = r.Feature, r.Tiers
	return p
}
