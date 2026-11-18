package bitbucketcloudfake

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

func (f *Fake) hooksFor(key string) []Hook {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneHooks(f.webhooks[key])
}

func (f *Fake) listWebhooks(w http.ResponseWriter, r *http.Request) {
	repo := r.Context().Value(repoKey).(*repository) //nolint:forcetypeassert // member sets it
	hooks := f.hooksFor(repo.workspace + "/" + repo.slug)
	out := make([]map[string]any, 0, len(hooks))
	for _, hook := range hooks {
		out = append(out, map[string]any{"url": hook.URL, "events": hook.Events, "active": hook.Active})
	}
	paged(w, r, out)
}
