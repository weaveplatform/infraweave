package gitmem

import (
	"net/http"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
)

// ServeUploadPack serves this repository through Git's read-only smart HTTP
// protocol. The provider fake authenticates and selects the repository first.
// Like the rawgit contract server, it serves whole histories without advertising
// shallow support; clients can exercise their full-fetch fallback.
func (r *Repo) ServeUploadPack(w http.ResponseWriter, req *http.Request) {
	advertise := req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/info/refs") &&
		req.URL.Query().Get("service") == "git-upload-pack"
	upload := req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/git-upload-pack")
	if !advertise && !upload {
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, err := transport.NewEndpoint("https://git.test/repo.git")
	if err != nil {
		http.Error(w, "invalid Git endpoint", http.StatusInternalServerError)
		return
	}
	session, err := server.NewServer(server.MapLoader{
		endpoint.String(): r.git.Storer,
	}).NewUploadPackSession(endpoint, nil)
	if err != nil {
		http.Error(w, "cannot open Git repository", http.StatusInternalServerError)
		return
	}
	defer session.Close()
	if advertise {
		refs, err := session.AdvertisedReferencesContext(req.Context())
		if err != nil {
			http.Error(w, "cannot read Git references", http.StatusInternalServerError)
			return
		}
		refs.Prefix = [][]byte{[]byte("# service=git-upload-pack"), pktline.Flush}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		_ = refs.Encode(w)
		return
	}
	request := packp.NewUploadPackRequest()
	if err := request.Decode(req.Body); err != nil {
		http.Error(w, "invalid upload-pack request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	response, err := session.UploadPack(req.Context(), request)
	if err != nil {
		_ = pktline.NewEncoder(w).EncodeString("ERR upload-pack: not our ref\n")
		return
	}
	defer response.Close()
	_ = response.Encode(w)
}
