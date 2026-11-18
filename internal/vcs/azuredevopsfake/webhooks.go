package azuredevopsfake

import (
	"net/http"
	"slices"
)

// Hook is one configured webhook, with no stored secret or credential.
type Hook struct {
	URL    string
	Events []string
	Active bool
}

// SetHooks replaces the repository's configured hooks. Inputs are copied
// so concurrent provider requests observe a complete fixture snapshot.
func (f *Fake) SetHooks(namespace, name string, hooks []Hook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.webhooks == nil {
		f.webhooks = map[string][]Hook{}
	}
	f.webhooks[namespace+"/"+name] = cloneHooks(hooks)
}

func cloneHooks(hooks []Hook) []Hook {
	out := slices.Clone(hooks)
	for i := range out {
		out[i].Events = slices.Clone(out[i].Events)
	}
	return out
}

func (f *Fake) listWebhooks(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	out := []map[string]any{}
	for project, p := range f.projects {
		for name, repo := range p.repos {
			for _, hook := range f.webhooks[project+"/"+name] {
				status := "disabled"
				if hook.Active {
					status = "enabled"
				}
				for _, event := range hook.Events {
					out = append(out, map[string]any{
						"publisherId": "tfs", "eventType": event, "status": status,
						"publisherInputs": map[string]string{"projectId": p.id, "repository": repo.id},
						"consumerId":      "webHooks", "consumerActionId": "httpRequest",
						"consumerInputs": map[string]string{"url": hook.URL},
					})
				}
			}
		}
	}
	f.mu.Unlock()
	answer(w, http.StatusOK, map[string]any{"count": len(out), "value": out})
}
