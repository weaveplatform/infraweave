package gitlab

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// Only payload facts used by normalization are decoded. GitLab can add other
// fields without changing the consumed event, and no enrichment call is needed.
//
//nolint:tagliatelle // GitLab's documented wire names.
type webhookPayload struct {
	ObjectKind   string               `json:"object_kind"`
	Project      webhookProject       `json:"project"`
	Attributes   *webhookAttributes   `json:"object_attributes"`
	MergeRequest *webhookRequest      `json:"merge_request"`
	Before       string               `json:"before"`
	After        string               `json:"after"`
	Ref          string               `json:"ref"`
	UserUsername string               `json:"user_username"`
	UserName     string               `json:"user_name"`
	Commits      *[]webhookPushCommit `json:"commits"`
	TotalCommits *int64               `json:"total_commits_count"`
	Labels       []webhookLabel       `json:"labels"`
	User         struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
	Changes struct {
		Labels *webhookLabelChange `json:"labels"`
	} `json:"changes"`
}

//nolint:tagliatelle // GitLab's documented wire names.
type webhookProject struct {
	WebURL            string `json:"web_url"`
	Homepage          string `json:"homepage"`
	ID                int64  `json:"id"`
	Namespace         string `json:"namespace"`
	PathWithNamespace string `json:"path_with_namespace"`
}

func (p webhookProject) repository() (vcsapi.Repository, bool) {
	namespace, name, ok := strings.Cut(p.PathWithNamespace, "/")
	if last := strings.LastIndex(p.PathWithNamespace, "/"); last >= 0 {
		namespace, name = p.PathWithNamespace[:last], p.PathWithNamespace[last+1:]
	}
	if p.ID <= 0 || !ok || namespace == "" || name == "" {
		return vcsapi.Repository{}, false
	}
	return vcsapi.Repository{ID: strconv.FormatInt(p.ID, 10), Namespace: namespace, Name: name}, true
}

func (p webhookProject) namespace() string {
	if p.Namespace != "" {
		return p.Namespace
	}
	if last := strings.LastIndex(p.PathWithNamespace, "/"); last > 0 {
		return p.PathWithNamespace[:last]
	}
	return ""
}

type webhookLabel struct {
	Title string `json:"title"`
}

type webhookLabelChange struct {
	Previous []webhookLabel `json:"previous"`
	Current  []webhookLabel `json:"current"`
}

//nolint:tagliatelle,misspell // GitLab's documented wire names include noteable_type.
type webhookAttributes struct {
	webhookRequest
	Action       string  `json:"action"`
	OldRev       string  `json:"oldrev"`
	NoteableType string  `json:"noteable_type"`
	Note         *string `json:"note"`
}

//nolint:tagliatelle // GitLab's documented wire names.
type webhookRequest struct {
	AuthorID                    int64           `json:"author_id"`
	Title                       string          `json:"title"`
	URL                         string          `json:"url"`
	State                       string          `json:"state"`
	CreatedAt                   webhookTime     `json:"created_at"`
	UpdatedAt                   webhookTime     `json:"updated_at"`
	IID                         int             `json:"iid"`
	SourceProjectID             int64           `json:"source_project_id"`
	TargetProjectID             int64           `json:"target_project_id"`
	SourceBranch                string          `json:"source_branch"`
	TargetBranch                string          `json:"target_branch"`
	Source                      *webhookProject `json:"source"`
	Draft                       bool            `json:"draft"`
	Labels                      []webhookLabel  `json:"labels"`
	MergeStatus                 string          `json:"merge_status"`
	DetailedMergeStatus         string          `json:"detailed_merge_status"`
	BlockingDiscussionsResolved *bool           `json:"blocking_discussions_resolved"`
	LastCommit                  struct {
		ID string `json:"id"`
	} `json:"last_commit"`
}

func (r webhookRequest) request(project webhookProject, action string) (vcsapi.PullRequest, bool) {
	if r.IID <= 0 || r.SourceProjectID <= 0 || r.TargetProjectID != project.ID ||
		r.SourceBranch == "" || r.TargetBranch == "" || !commitID(r.LastCommit.ID) || strings.Trim(r.LastCommit.ID, "0") == "" {
		return vcsapi.PullRequest{}, false
	}
	owner := ""
	if r.Source != nil {
		owner = r.Source.namespace()
	} else if r.SourceProjectID == r.TargetProjectID {
		owner = project.namespace()
	}
	approved := action == "approved"
	resolved := r.BlockingDiscussionsResolved != nil && *r.BlockingDiscussionsResolved
	// Legacy merge_status alone proves no conflicts, not approval or resolved
	// discussions. The fully-approved action supplies that missing approval fact.
	mergeable := !r.Draft && action != "unapproved" && resolved && (r.DetailedMergeStatus == "mergeable" ||
		r.DetailedMergeStatus == "" && r.MergeStatus == "can_be_merged" && approved)
	out := vcsapi.PullRequest{
		Number: r.IID, HeadBranch: r.SourceBranch, BaseBranch: r.TargetBranch,
		HeadOwner: owner, Fork: r.SourceProjectID != r.TargetProjectID,
		Draft: r.Draft, Approved: approved, Mergeable: mergeable, Labels: labelNames(r.Labels),
	}
	if r.AuthorID > 0 {
		out.AuthorID = strconv.FormatInt(r.AuthorID, 10)
	}
	out.Title, out.URL = r.Title, r.URL
	out.CreatedAt, out.UpdatedAt = r.CreatedAt.Time, r.UpdatedAt.Time
	switch r.State {
	case "opened", "locked":
		out.State = "open"
	case "closed", "merged":
		out.State = r.State
	}
	if r.Source != nil {
		if base := or(r.Source.WebURL, r.Source.Homepage); base != "" {
			out.HeadBranchURL = strings.TrimSuffix(base, "/") + "/-/tree/" + url.PathEscape(r.SourceBranch)
		}
	}
	return out, true
}

// GitLab before19 used a named timezone rather than an ISO timestamp.
type webhookTime struct{ time.Time }

func (t *webhookTime) UnmarshalJSON(body []byte) error {
	var raw string
	if err := json.Unmarshal(body, &raw); err != nil {
		return errWebhookPayload
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			t.Time = parsed
			return nil
		}
	}
	return errWebhookPayload
}

//nolint:tagliatelle,misspell // GitLab's documented routing field spellings.
type webhookRouting struct {
	Attributes *struct {
		Action       string `json:"action"`
		NoteableType string `json:"noteable_type"`
	} `json:"object_attributes"`
}
