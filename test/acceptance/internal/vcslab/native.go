package vcslab

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrNative reports only operation and status; provider response bodies, URLs
// and credentials are never rendered into the lab's public failures.
var ErrNative = errors.New("vcs lab: native provider verification failed")

// Native is the lab's verification client, independent of product adapters.
// GitHub uses the existing repository-administration token for lab mutations;
// the served integration independently authenticates through the dedicated App.
type Native struct {
	Provider, Host, Namespace, Repository, User string
	token                                       string
	client                                      *http.Client
}

// NativeConfig names one disposable first repository and its existing lab token.
// Formatting must not be used for this credential-bearing configuration.
type NativeConfig struct{ Provider, Host, Namespace, Repository, User, Token, CAFile string }

func (NativeConfig) String() string             { return "[native lab configuration]" }
func (NativeConfig) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte("[native lab configuration]")) }
func (Native) String() string                   { return "[native lab client]" }
func (Native) Format(s fmt.State, _ rune)       { _, _ = s.Write([]byte("[native lab client]")) }

// NewNative validates identity and trusts only the job's explicit extra CA.
// Redirects are refused so an unexpected Location cannot forward credentials.
func NewNative(in NativeConfig) (*Native, error) {
	if in.Namespace == "" || in.Repository == "" || in.Token == "" {
		return nil, ErrNative
	}
	base := strings.TrimRight(in.Host, "/")
	switch in.Provider {
	case "github":
		base = "https://api.github.com"
	case "bitbucket-cloud":
		base = "https://api.bitbucket.org/2.0"
	case "gitlab", "bitbucket-dc", "azure-devops":
	default:
		return nil, ErrNative
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrNative
	}
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // Standard library default transport.
	if in.CAFile != "" {
		pem, err := os.ReadFile(in.CAFile)
		if err != nil {
			return nil, ErrNative
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, ErrNative
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, ErrNative
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &Native{
		Provider:   in.Provider,
		Host:       base,
		Namespace:  in.Namespace,
		Repository: in.Repository,
		User:       in.User,
		token:      in.Token,
		client:     &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (n *Native) repoPath() string {
	ns, repo := url.PathEscape(n.Namespace), url.PathEscape(n.Repository)
	switch n.Provider {
	case "github":
		return "/repos/" + ns + "/" + repo
	case "gitlab":
		return "/api/v4/projects/" + url.PathEscape(n.Namespace+"/"+n.Repository)
	case "bitbucket-cloud":
		return "/repositories/" + ns + "/" + repo
	case "bitbucket-dc":
		return "/rest/api/1.0/projects/" + ns + "/repos/" + repo
	default:
		return "/" + ns + "/_apis/git/repositories/" + repo
	}
}

func (n *Native) send(ctx context.Context, method, path string, body, out any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return ErrNative
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, n.Host+path, bytes.NewReader(encoded))
	if err != nil {
		return ErrNative
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	switch n.Provider {
	case "github":
		req.Header.Set("Authorization", "Bearer "+n.token)
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	case "gitlab":
		req.Header.Set("PRIVATE-TOKEN", n.token)
	case "bitbucket-cloud":
		req.SetBasicAuth(n.User, n.token)
	case "bitbucket-dc":
		req.Header.Set("Authorization", "Bearer "+n.token)
	case "azure-devops":
		req.SetBasicAuth("", n.token)
	}
	response, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: native transport", ErrNative)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%w: status%d", ErrNative, response.StatusCode)
	}
	if out != nil && json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(out) != nil {
		return fmt.Errorf("%w: invalid native response", ErrNative)
	}
	return nil
}

// OpenPullRequest creates a real native PR/MR; no direct intake or database
// insertion can substitute for the provider's own signed delivery.
func (n *Native) OpenPullRequest(ctx context.Context, branch string) (int, error) {
	path := n.repoPath()
	var body any
	switch n.Provider {
	case "github":
		path += "/pulls"
		body = map[string]any{"title": "VCS lab verification", "head": branch, "base": "main"}
	case "gitlab":
		path += "/merge_requests"
		body = map[string]any{"title": "VCS lab verification", "source_branch": branch, "target_branch": "main"}
	case "bitbucket-cloud":
		path += "/pullrequests"
		body = map[string]any{
			"title":       "VCS lab verification",
			"source":      map[string]any{"branch": map[string]string{"name": branch}},
			"destination": map[string]any{"branch": map[string]string{"name": "main"}},
		}
	case "bitbucket-dc":
		path += "/pull-requests"
		repo := map[string]any{"slug": n.Repository, "project": map[string]string{"key": n.Namespace}}
		body = map[string]any{
			"title":   "VCS lab verification",
			"fromRef": map[string]any{"id": "refs/heads/" + branch, "repository": repo},
			"toRef":   map[string]any{"id": "refs/heads/main", "repository": repo},
		}
	case "azure-devops":
		path += "/pullrequests?api-version=7.1"
		body = map[string]any{"title": "VCS lab verification", "sourceRefName": "refs/heads/" + branch, "targetRefName": "refs/heads/main"}
	}
	var answer struct {
		ID            int `json:"id"`
		Number        int `json:"number"`
		IID           int `json:"iid"`
		PullRequestID int `json:"pullRequestId"`
	}
	if err := n.send(ctx, http.MethodPost, path, body, &answer); err != nil {
		return 0, err
	}
	id := answer.ID
	switch n.Provider {
	case "github":
		id = answer.Number
	case "gitlab":
		id = answer.IID
	case "azure-devops":
		id = answer.PullRequestID
	}
	if id <= 0 {
		return 0, ErrNative
	}
	return id, nil
}

func (n *Native) commentsPath(number int) string {
	id := strconv.Itoa(number)
	switch n.Provider {
	case "github":
		return n.repoPath() + "/issues/" + id + "/comments"
	case "gitlab":
		return n.repoPath() + "/merge_requests/" + id + "/notes"
	case "bitbucket-cloud":
		return n.repoPath() + "/pullrequests/" + id + "/comments"
	case "bitbucket-dc":
		return n.repoPath() + "/pull-requests/" + id + "/comments"
	default:
		return n.repoPath() + "/pullRequests/" + id + "/threads"
	}
}

// Comment sends ordinary text only. Lab callers do not issue comment commands or
// requested-action/button events, which remain outside this verification scope.
func (n *Native) Comment(ctx context.Context, number int, text string) error {
	if number <= 0 {
		return ErrNative
	}
	path := n.commentsPath(number)
	var body any
	switch n.Provider {
	case "github", "gitlab":
		body = map[string]string{"body": text}
	case "bitbucket-cloud":
		body = map[string]any{"content": map[string]string{"raw": text}}
	case "bitbucket-dc":
		body = map[string]string{"text": text}
	case "azure-devops":
		path += "?api-version=7.1"
		body = map[string]any{"comments": []map[string]any{{"parentCommentId": 0, "content": text, "commentType": 1}}, "status": 1}
	}
	return n.send(ctx, http.MethodPost, path, body, nil)
}
