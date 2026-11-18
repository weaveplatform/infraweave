package githubfake

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

var (
	errRepositoryRenameRefused  = errors.New("githubfake: repository rename refused")
	errOwnerRenameRefused       = errors.New("githubfake: owner rename refused")
	errInstallationMissing      = errors.New("githubfake: installation missing")
	errInvalidInstallationState = errors.New("githubfake: invalid installation state")
	errRepositoryMissing        = errors.New("githubfake: repository missing")
)

// RenameRepository changes only native coordinates, preserving repository ID,
// Git history and installation membership.
func (f *Fake) RenameRepository(owner, from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, next := owner+"/"+from, owner+"/"+to
	repo, found := f.repos[old]
	if !found || f.repos[next] != nil {
		return errRepositoryRenameRefused
	}
	delete(f.repos, old)
	f.repos[next] = repo
	for _, in := range f.installs {
		if in.Owner != owner {
			continue
		}
		for i, name := range in.Repos {
			if name == from {
				in.Repos[i] = to
			}
		}
	}
	return nil
}

// RenameOwner preserves the native owner ID and every repository ID.
func (f *Fake) RenameOwner(from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ownerIDs[from] == 0 || f.ownerIDs[to] != 0 {
		return errOwnerRenameRefused
	}
	f.ownerIDs[to] = f.ownerIDs[from]
	delete(f.ownerIDs, from)
	for _, in := range f.installs {
		if in.Owner == from {
			in.Owner = to
		}
	}
	for path, repo := range f.repos {
		if len(path) > len(from) && path[:len(from)+1] == from+"/" {
			delete(f.repos, path)
			f.repos[to+path[len(from):]] = repo
		}
	}
	return nil
}

// SetInstallationState changes the native API's availability. Deleted
// installations disappear from GET /app/installations.
func (f *Fake) SetInstallationState(id int64, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.installs[id]
	if in == nil {
		return errInstallationMissing
	}
	switch state {
	case "active":
		in.Deleted, in.SuspendedAt = false, nil
	case "suspended":
		at := time.Now().UTC()
		in.Deleted, in.SuspendedAt = false, &at
	case "deleted":
		in.Deleted, in.SuspendedAt = true, nil
	default:
		return errInvalidInstallationState
	}
	return nil
}

// SetInstallationRepositories changes the real installation's reachable set.
func (f *Fake) SetInstallationRepositories(id int64, repos ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installs[id] == nil {
		return errInstallationMissing
	}
	f.installs[id].Repos = slices.Clone(repos)
	return nil
}

// RepositoryPayload is native repository JSON with real IDs, suitable for
// documented GitHub webhook payloads. It does not create a missing repository.
func (f *Fake) RepositoryPayload(owner, name string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo := f.repos[owner+"/"+name]
	if repo == nil {
		return nil, errRepositoryMissing
	}
	return encodeLifecycle(map[string]any{"id": f.repoIDs[repo], "name": name, "full_name": owner + "/" + name, //nolint:wrapcheck // the called boundary names the failed operation
		"owner": map[string]any{"id": f.ownerIDs[owner], "login": owner}})
}

// InstallationPayload is native installation JSON, including a deleted record
// still available to the webhook fixture even though API listing omits it.
func (f *Fake) InstallationPayload(id int64) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.installs[id]
	if in == nil {
		return nil, errInstallationMissing
	}
	return encodeLifecycle(
		map[string]any{"id": in.ID, "account": map[string]any{"id": in.OwnerID, "login": in.Owner, "type": in.OwnerType}, "suspended_at": in.SuspendedAt},
	) //nolint:wrapcheck // the called boundary names the failed operation
}

func encodeLifecycle(value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("githubfake: encode native lifecycle payload: %w", err)
	}
	return body, nil
}
