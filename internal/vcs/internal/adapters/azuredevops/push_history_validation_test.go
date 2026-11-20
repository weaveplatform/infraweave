package azuredevops

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type historyBody string

func (body historyBody) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestUnitPushChangesKeepBothRenamePathsAndSkipFolders(t *testing.T) {
	p := newProvider(historyBody(`{"changeCounts":{"Add":1,"Rename":1,"Edit":1},"changes":[
	 {"item":{"path":"/dir","isFolder":true}},
	 {"item":{"path":"/dir/new.tf"},"originalPath":"/dir/old.tf","sourceServerItem":"/dir/legacy.tf"},
	 {"item":{"path":"/other.tf"}}]}`))
	in := port.Integration{Settings: port.Settings{OrganizationURL: "https://dev.azure.com/test"}, Credential: []byte("test-token")}
	got, err := p.pushCommitFiles(t.Context(), in, "/project/_apis/git/repositories/repo", strings.Repeat("a", 40))
	if err != nil || !slices.Equal(got, []string{"dir/legacy.tf", "dir/new.tf", "dir/old.tf", "other.tf"}) {
		t.Fatal("native rename or folder semantics were lost", got, err)
	}
}

func TestUnitPushChangesRefuseIncompleteValues(t *testing.T) {
	for name, body := range map[string]historyBody{
		"missing changes": `{ "changeCounts":{} }`,
		"missing counts":  `{"changes":[]}`,
		"negative count":  `{"changeCounts":{"Add":-1},"changes":[]}`,
		"truncated":       `{"changeCounts":{"Add":1},"changes":[]}`,
		"count mismatch":  `{"changeCounts":{},"changes":[{"item":{"path":"/main.tf"}}]}`,
		"missing path":    `{"changeCounts":{"Add":1},"changes":[{"item":{}}]}`,
		"repeated path":   `{"changeCounts":{"Add":2},"changes":[{"item":{"path":"/main.tf"}},{"item":{"path":"/main.tf"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := newProvider(body)
			in := port.Integration{Settings: port.Settings{OrganizationURL: "https://dev.azure.com/test"}, Credential: []byte("test-token")}
			got, err := p.pushCommitFiles(t.Context(), in, "/project/_apis/git/repositories/repo", strings.Repeat("a", 40))
			if !errors.Is(err, kernel.ErrUnavailable) || got != nil {
				t.Fatal("unknown native changes became complete", got, err)
			}
		})
	}
}
