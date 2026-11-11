package gitlab

import (
	"net/http"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// NewOver is New sending through rt, for a test that serves the fake in
// memory.
func NewOver(rt http.RoundTripper) *Provider { return newProvider(rt) }

// Hosts is the API base and user-facing host of s.
func Hosts(s port.Settings) (api, web string, err error) {
	h, err := hostOf(s)
	return h.api, h.web, err
}
