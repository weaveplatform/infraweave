//nolint:tagliatelle // Native provider readback schemas define their wire field names.
package vcslab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// NativeCheck is a read-back fact, not the VCS job's local report receipt.
type NativeCheck struct{ ID, Name, SHA, State, URL, ExternalID string }

// CurrentCheck selects the native resource whose current facts may be asserted.
// GitHub must have exactly one resource for this run. Shared status APIs choose
// the newest logical context before testing its owner, never an older success.
func CurrentCheck(provider string, items []NativeCheck, runURL, stack string) (NativeCheck, bool, error) {
	var selected NativeCheck
	count := 0
	latest := int64(-1)
	for _, item := range items {
		switch provider {
		case "github":
			if item.URL != runURL {
				continue
			}
			count++
			if item.ExternalID != stack {
				return NativeCheck{}, false, fmt.Errorf("%w: current check stack mismatch", ErrNative)
			}
			selected = item
		case "gitlab", "azure-devops":
			id, err := strconv.ParseInt(item.ID, 10, 64)
			if err != nil || id <= 0 {
				return NativeCheck{}, false, ErrNative
			}
			if id > latest {
				latest = id
				selected = item
				count = 1
			}
		default:
			selected = item
			count++
		}
	}
	if count == 0 {
		return NativeCheck{}, false, nil
	}
	if count != 1 {
		return NativeCheck{}, false, fmt.Errorf("%w: %d current check resources", ErrNative, count)
	}
	return selected, selected.URL == runURL, nil
}

// Checks reads native resources for exactly this repository/head/name. GitHub
// has a separate resource per run. GitLab/Azure retain native POST history; their
// latest logical named context is the resource whose ownership the lab checks.
func (n *Native) Checks(ctx context.Context, sha, name string) ([]NativeCheck, error) {
	path := n.repoPath()
	var result []NativeCheck
	switch n.Provider {
	case "github":
		for page := 1; ; page++ {
			var response struct {
				Items []struct {
					ID         int64  `json:"id"`
					Name       string `json:"name"`
					SHA        string `json:"head_sha"`
					Status     string `json:"status"`
					Conclusion string `json:"conclusion"`
					URL        string `json:"details_url"`
					ExternalID string `json:"external_id"`
					App        struct {
						ID int64 `json:"id"`
					} `json:"app"`
				} `json:"check_runs"`
			}
			query := url.Values{"filter": {"all"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}, "app_id": {n.User}}
			if err := n.send(ctx, http.MethodGet, path+"/commits/"+url.PathEscape(sha)+"/check-runs?"+query.Encode(), nil, &response); err != nil {
				return nil, err
			}
			appID, err := strconv.ParseInt(n.User, 10, 64)
			if err != nil {
				return nil, ErrNative
			}
			for _, v := range response.Items {
				if v.Name == name && v.SHA == sha && v.App.ID == appID {
					state := v.Status
					if v.Conclusion != "" {
						state = v.Conclusion
					}
					result = append(result, NativeCheck{ID: strconv.FormatInt(v.ID, 10), Name: v.Name, SHA: v.SHA, State: state, URL: v.URL, ExternalID: v.ExternalID})
				}
			}
			if len(response.Items) < 100 {
				break
			}
		}
	case "gitlab":
		for page := 1; ; page++ {
			var response []struct {
				ID    int64  `json:"id"`
				Name  string `json:"name"`
				SHA   string `json:"sha"`
				State string `json:"status"`
				URL   string `json:"target_url"`
			}
			query := url.Values{"name": {name}, "all": {"false"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
			if err := n.send(ctx, http.MethodGet, path+"/repository/commits/"+url.PathEscape(sha)+"/statuses?"+query.Encode(), nil, &response); err != nil {
				return nil, err
			}
			for _, v := range response {
				if v.Name == name && v.SHA == sha {
					result = append(result, NativeCheck{ID: strconv.FormatInt(v.ID, 10), Name: v.Name, SHA: v.SHA, State: v.State, URL: v.URL})
				}
			}
			if len(response) < 100 {
				break
			}
		}
	case "bitbucket-cloud", "bitbucket-dc":
		var response struct {
			Key   string `json:"key"`
			Name  string `json:"name"`
			State string `json:"state"`
			URL   string `json:"url"`
		}
		if n.Provider == "bitbucket-cloud" {
			path += "/commit/" + url.PathEscape(sha) + "/statuses/build/" + url.PathEscape(name)
		} else {
			path += "/commits/" + url.PathEscape(sha) + "/builds?" + url.Values{"key": {name}}.Encode()
		}
		if err := n.send(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		if response.Key != name || response.Name != name {
			return nil, ErrNative
		}
		result = append(result, NativeCheck{ID: response.Key, Name: response.Name, SHA: sha, State: response.State, URL: response.URL})
	case "azure-devops":
		for skip := 0; ; skip += 100 {
			var response struct {
				Items []struct {
					ID      int64  `json:"id"`
					State   string `json:"state"`
					URL     string `json:"targetUrl"`
					Context struct {
						Name  string `json:"name"`
						Genre string `json:"genre"`
					} `json:"context"`
				} `json:"value"`
			}
			query := url.Values{"api-version": {"7.1"}, "latestOnly": {"true"}, "top": {"100"}, "skip": {strconv.Itoa(skip)}}
			if err := n.send(ctx, http.MethodGet, path+"/commits/"+url.PathEscape(sha)+"/statuses?"+query.Encode(), nil, &response); err != nil {
				return nil, err
			}
			for _, v := range response.Items {
				if v.Context.Name == name && v.Context.Genre == "infraweave" {
					result = append(result, NativeCheck{ID: strconv.FormatInt(v.ID, 10), Name: name, SHA: sha, State: v.State, URL: v.URL})
				}
			}
			if len(response.Items) < 100 {
				break
			}
		}
	}
	return result, nil
}

// NativeComment is the provider's persisted comment, including GraphQL identity
// when GitHub can minimize earlier comments.
type NativeComment struct{ ID, NodeID, Body string }

// Comments reads every native page, rejecting cross-origin continuations rather
// than forwarding the lab credential. A disposable PR normally has few comments,
// but the assertion never silently truncates at its first page.
func (n *Native) Comments(ctx context.Context, number int) ([]NativeComment, error) {
	base := n.commentsPath(number)
	path := base
	page, start := 1, 0
	seen := map[string]bool{}
	var result []NativeComment
	for {
		switch n.Provider {
		case "github", "gitlab":
			path = base + "?per_page=100&page=" + strconv.Itoa(page)
		case "bitbucket-cloud":
			if page == 1 {
				path = base + "?pagelen=100"
			}
		case "bitbucket-dc":
			path = base + "?limit=100&start=" + strconv.Itoa(start)
		case "azure-devops":
			path = base + "?api-version=7.1"
		}
		if seen[path] {
			return nil, ErrNative
		}
		seen[path] = true
		var raw json.RawMessage
		if err := n.send(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
		var items []json.RawMessage
		next := ""
		var nextStart *int
		last := true
		if n.Provider == "github" || n.Provider == "gitlab" {
			if json.Unmarshal(raw, &items) != nil {
				return nil, ErrNative
			}
			last = len(items) < 100
		} else {
			var envelope struct {
				Values []json.RawMessage `json:"values"`
				Value  []json.RawMessage `json:"value"`
				Next   string            `json:"next"`
				Last   *bool             `json:"isLastPage"`
				Start  *int              `json:"nextPageStart"`
			}
			if json.Unmarshal(raw, &envelope) != nil {
				return nil, ErrNative
			}
			items = envelope.Values
			if n.Provider == "azure-devops" {
				items = envelope.Value
			}
			next, nextStart = envelope.Next, envelope.Start
			if n.Provider == "bitbucket-cloud" {
				last = next == ""
			}
			if n.Provider == "bitbucket-dc" {
				if envelope.Last == nil {
					return nil, ErrNative
				}
				last = *envelope.Last
			}
		}
		for _, item := range items {
			var v struct {
				ID      int64  `json:"id"`
				Node    string `json:"node_id"`
				Body    string `json:"body"`
				Text    string `json:"text"`
				Content struct {
					Raw string `json:"raw"`
				} `json:"content"`
				Deleted  bool `json:"isDeleted"`
				Comments []struct {
					ID      int64  `json:"id"`
					Content string `json:"content"`
					Deleted bool   `json:"isDeleted"`
				} `json:"comments"`
			}
			if json.Unmarshal(item, &v) != nil || v.ID <= 0 {
				return nil, ErrNative
			}
			if n.Provider == "azure-devops" {
				for _, c := range v.Comments {
					if !v.Deleted && !c.Deleted {
						result = append(result, NativeComment{ID: strconv.FormatInt(v.ID, 10) + "/" + strconv.FormatInt(c.ID, 10), Body: c.Content})
					}
				}
				continue
			}
			body := v.Body
			if n.Provider == "bitbucket-cloud" {
				body = v.Content.Raw
			}
			if n.Provider == "bitbucket-dc" {
				body = v.Text
			}
			result = append(result, NativeComment{ID: strconv.FormatInt(v.ID, 10), NodeID: v.Node, Body: body})
		}
		if last {
			break
		}
		page++
		if n.Provider == "bitbucket-cloud" {
			if !strings.HasPrefix(next, n.Host+base+"?") {
				return nil, ErrNative
			}
			path = strings.TrimPrefix(next, n.Host)
		}
		if n.Provider == "bitbucket-dc" {
			if nextStart == nil || *nextStart <= start {
				return nil, ErrNative
			}
			start = *nextStart
		}
	}
	return result, nil
}

// Minimized reads GitHub's actual GraphQL state instead of assuming a successful
// product job receipt means the previous comment is hidden.
func (n *Native) Minimized(ctx context.Context, node string) (bool, error) {
	if n.Provider != "github" || node == "" {
		return false, ErrNative
	}
	var response struct {
		Data struct {
			Node struct {
				ID        string `json:"id"`
				Minimized *bool  `json:"isMinimized"`
				Reason    string `json:"minimizedReason"`
			} `json:"node"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	query := map[string]any{"query": `query($id:ID!){node(id:$id){... on IssueComment{id isMinimized minimizedReason}}}`, "variables": map[string]string{"id": node}}
	if err := n.send(ctx, http.MethodPost, "/graphql", query, &response); err != nil {
		return false, err
	}
	v := response.Data.Node
	if len(response.Errors) > 0 || v.ID != node || v.Minimized == nil {
		return false, ErrNative
	}
	return *v.Minimized && v.Reason == "outdated", nil
}
