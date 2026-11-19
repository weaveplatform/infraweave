package githubfake

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SetCollaboratorPermission changes only the native user's repository role.
// Unknown users have no access. Effective permissions use GitHub's base roles.
func (f *Fake) SetCollaboratorPermission(owner, repo, login, permission string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.collabs == nil {
		f.collabs = map[string]string{}
	}
	f.collabs[owner+"/"+repo+"/"+login] = permission
}

func (f *Fake) collaboratorPermission(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	permission := f.collabs[deploymentRepo(r)+"/"+chi.URLParam(r, "username")]
	f.mu.Unlock()
	if permission == "" {
		permission = "none"
	}
	answer(w, http.StatusOK, map[string]any{"permission": permission, "user": map[string]string{"login": chi.URLParam(r, "username")}})
}

// CheckActionPayload builds a requested_action delivery for an actual recorded
// check. Its immutable SHA, external ID and App owner come from that record;
// tests can mutate the returned JSON separately to exercise forged deliveries.
func (f *Fake) CheckActionPayload(owner, repo string, id int64, action, login string) ([]byte, error) {
	body, err := f.RepositoryPayload(owner, repo)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	var found *CheckRecord
	for _, r := range f.checks.records[owner+"/"+repo] {
		if r.ID == id {
			copy := r
			found = &copy
			break
		}
	}
	f.mu.Unlock()
	if found == nil {
		return nil, ErrDelivery
	}
	out := map[string]any{
		"action": "requested_action", "repository": json.RawMessage(body),
		"sender":           map[string]any{"id": 17, "login": login, "email": "sender@example.com"},
		"check_run":        map[string]any{"id": found.ID, "head_sha": found.SHA, "external_id": found.ExternalID, "app": map[string]int64{"id": found.App.ID}},
		"requested_action": map[string]string{"identifier": action},
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, ErrDelivery
	}
	return encoded, nil
}

// PressCheck sends one signed press from the App that owns the recorded check.
// A deliberate new press has a new delivery ID; Sender.Redeliver preserves it.
//
//nolint:tagliatelle // GitHub owns check_run
func (f *Fake) PressCheck(ctx context.Context, target, delivery, owner, repo string, id int64, action, login string) (int, error) {
	body, err := f.CheckActionPayload(owner, repo, id, action, login)
	if err != nil {
		return 0, err
	}
	var payload struct {
		CheckRun struct {
			App struct {
				ID int64 `json:"id"`
			} `json:"app"`
		} `json:"check_run"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return 0, ErrDelivery
	}
	sender, err := f.Sender(payload.CheckRun.App.ID)
	if err != nil {
		return 0, err
	}
	return sender.Send(ctx, target, delivery, "check_run", body)
}
