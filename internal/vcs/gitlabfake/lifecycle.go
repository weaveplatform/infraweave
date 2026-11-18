package gitlabfake

import "errors"

// RenameProject changes a project's path without replacing its native identity,
// history, membership or access tokens.
func (f *Fake) RenameProject(namespace, name, nextNamespace, nextName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, next := namespace+"/"+name, nextNamespace+"/"+nextName
	project, found := f.projects[old]
	if !found || f.projects[next] != nil {
		return errors.New("gitlabfake: project rename refused")
	}
	delete(f.projects, old)
	project.namespace, project.path = nextNamespace, nextName
	f.projects[next] = project
	return nil
}
