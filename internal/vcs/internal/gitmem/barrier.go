package gitmem

import "context"

type uploadBarrier struct {
	release chan struct{}
	active  int
}

// HoldUploadPacks pauses authenticated fake Git fetches before they read the
// graph. Provider fakes select/authenticate their repository before invoking
// ServeUploadPack, so invalid credentials cannot enter this barrier.
func (r *Repo) HoldUploadPacks() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uploads == nil {
		r.uploads = &uploadBarrier{release: make(chan struct{})}
	}
}

// ReleaseUploadPacks resumes every held fetch. Repeated release is harmless.
func (r *Repo) ReleaseUploadPacks() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uploads != nil {
		close(r.uploads.release)
		r.uploads = nil
	}
}

// HeldUploadPacks is the number of requests blocked by the current barrier.
func (r *Repo) HeldUploadPacks() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uploads == nil {
		return 0
	}
	return r.uploads.active
}

func (r *Repo) waitUpload(ctx context.Context) bool {
	r.mu.Lock()
	barrier := r.uploads
	if barrier == nil {
		r.mu.Unlock()
		return true
	}
	barrier.active++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); barrier.active--; r.mu.Unlock() }()
	select {
	case <-barrier.release:
		return true
	case <-ctx.Done():
		return false
	}
}
