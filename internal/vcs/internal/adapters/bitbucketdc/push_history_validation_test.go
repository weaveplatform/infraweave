package bitbucketdc

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type historyBody string

func (body historyBody) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestUnitPushHistoryRefusesMissingNativeValues(t *testing.T) {
	p := newProvider(historyBody(`{"isLastPage":true,"values":null}`))
	repo := port.Repository{
		Namespace:   "PROJECT",
		Name:        "repo",
		Integration: &port.Integration{Settings: port.Settings{APIHost: "https://dc.example"}, Credential: []byte("test-token")},
	}
	got, err := p.RecoverPush(t.Context(), repo, strings.Repeat("a", 40), strings.Repeat("b", 40))
	if !errors.Is(err, kernel.ErrUnavailable) || got.Commits != nil {
		t.Fatal("missing native values became a complete empty range", got, err)
	}
}
