package azuredevops

import (
	"strings"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

type webhookEnvelope struct {
	CreatedDate time.Time        `json:"createdDate"`
	ID          string           `json:"id"`
	EventType   string           `json:"eventType"`
	Resource    *webhookResource `json:"resource"`
}

type webhookResource struct {
	webhookRequest
	RefUpdates  []webhookRefUpdate `json:"refUpdates"`
	PushedBy    webhookIdentity    `json:"pushedBy"`
	PullRequest *webhookRequest    `json:"pullRequest"`
	Comment     *struct {
		Content *string         `json:"content"`
		Author  webhookIdentity `json:"author"`
	} `json:"comment"`
}

type webhookRefUpdate struct {
	Name        string `json:"name"`
	OldObjectID string `json:"oldObjectId"`
	NewObjectID string `json:"newObjectId"`
}

type webhookRepository struct {
	RemoteURL string `json:"remoteUrl"`
	WebURL    string `json:"webUrl"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Project   struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
}

func (r *webhookRepository) repository() (vcsapi.Repository, bool) {
	if r == nil || r.ID == "" || r.Name == "" || r.Project.Name == "" {
		return vcsapi.Repository{}, false
	}
	return vcsapi.Repository{ID: r.ID, Namespace: r.Project.Name, Name: r.Name}, true
}

type webhookIdentity struct {
	ID          string `json:"id"`
	UniqueName  string `json:"uniqueName"`
	DisplayName string `json:"displayName"`
}

func (i webhookIdentity) login() string {
	if i.UniqueName != "" {
		return i.UniqueName
	}
	if i.ID != "" {
		return i.ID
	}
	return i.DisplayName
}

type webhookRequest struct {
	Title        string    `json:"title"`
	CreationDate time.Time `json:"creationDate"`
	ClosedDate   time.Time `json:"closedDate"`
	UpdatedAt    time.Time `json:"-"`
	Links        struct {
		Web struct {
			Href string `json:"href"`
		} `json:"web"`
	} `json:"_links"` //nolint:tagliatelle // Azure DevOps' native links envelope.
	Repository            *webhookRepository `json:"repository"`
	PullRequestID         int                `json:"pullRequestId"`
	SourceRefName         string             `json:"sourceRefName"`
	TargetRefName         string             `json:"targetRefName"`
	Status                string             `json:"status"`
	IsDraft               bool               `json:"isDraft"`
	MergeStatus           string             `json:"mergeStatus"`
	CreatedBy             webhookIdentity    `json:"createdBy"`
	LastMergeSourceCommit struct {
		CommitID string `json:"commitId"`
	} `json:"lastMergeSourceCommit"`
	ForkSource *struct {
		Repository *webhookRepository `json:"repository"`
	} `json:"forkSource"`
	Reviewers []struct {
		Vote int `json:"vote"`
	} `json:"reviewers"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (r webhookRequest) request() (vcsapi.PullRequest, bool) {
	head, headOK := strings.CutPrefix(r.SourceRefName, "refs/heads/")
	base, baseOK := strings.CutPrefix(r.TargetRefName, "refs/heads/")
	if r.PullRequestID <= 0 || r.Repository == nil || !headOK || !baseOK || head == "" || base == "" ||
		!webhookCommitID(r.LastMergeSourceCommit.CommitID) || strings.Trim(r.LastMergeSourceCommit.CommitID, "0") == "" {
		return vcsapi.PullRequest{}, false
	}
	owner := r.Repository.Project.ID
	if r.ForkSource != nil {
		owner = ""
		if r.ForkSource.Repository != nil {
			owner = r.ForkSource.Repository.Project.ID
		}
	}
	approved := false
	for _, reviewer := range r.Reviewers {
		if reviewer.Vote == 5 || reviewer.Vote == 10 {
			approved = true
		}
	}
	labels := make([]string, 0, len(r.Labels))
	for _, label := range r.Labels {
		if label.Name != "" {
			labels = append(labels, label.Name)
		}
	}
	// mergeStatus reports the preview merge, not every blocking policy.
	// Neither it nor a reviewer vote proves target ancestry or full policy
	// approval, so mergeability and undiverged remain unknown/false.
	out := vcsapi.PullRequest{
		Number: r.PullRequestID, HeadBranch: head, BaseBranch: base, HeadOwner: owner,
		Draft: r.IsDraft, Fork: r.ForkSource != nil, Approved: approved, Labels: labels,
	}
	azureMetadata(r, &out)
	return out, true
}
