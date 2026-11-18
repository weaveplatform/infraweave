package githubfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// CommentRecord is an independent native comment snapshot. Azure retains both
// containing thread and comment identities; GitHub retains its GraphQL node.
type CommentRecord struct {
	ID        int64
	NodeID    string
	ThreadID  int64
	Version   int64
	Body      string
	Minimized bool
}

// CommentWrite records accepted effects in order, including a create whose
// response the fake loses before the client can record the native identity.
type CommentWrite struct {
	ID           int64
	Action, Body string
}
type commentStore struct {
	records          map[string][]CommentRecord
	writes           map[string][]CommentWrite
	failCreate       bool
	minimizeAttempts map[string][]string
	failMinimize     bool
}

// Comments returns detached snapshots in native creation order.
func (f *Fake) Comments(namespace, repo, pr string) []CommentRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.comments.records[namespace+"/"+repo+"/"+pr])
}

// CommentWrites returns the accepted native effect order.
func (f *Fake) CommentWrites(namespace, repo, pr string) []CommentWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.comments.writes[namespace+"/"+repo+"/"+pr])
}

// FailNextCommentCreate loses one successful create response after persistence.
func (f *Fake) FailNextCommentCreate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments.failCreate = true
}

// SeedComment arranges an existing native comment, including human log examples.
func (f *Fake) SeedComment(namespace, repo, pr, body string) CommentRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newComment(namespace+"/"+repo+"/"+pr, body)
}

func (f *Fake) newComment(key, body string) CommentRecord {
	if f.comments.records == nil {
		f.comments.records = map[string][]CommentRecord{}
		f.comments.writes = map[string][]CommentWrite{}
	}
	f.nextID++
	record := CommentRecord{ID: int64(f.nextID), Body: body}
	record.NodeID = "IC_" + strconv.FormatInt(record.ID, 10)
	f.comments.records[key] = append(f.comments.records[key], record)
	f.commentWrite(key, record, "create")
	return record
}

func (f *Fake) commentWrite(key string, record CommentRecord, action string) {
	f.comments.writes[key] = append(f.comments.writes[key], CommentWrite{ID: record.ID, Action: action, Body: record.Body})
}

func (f *Fake) commentRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(f.commentPermission)
		r.Get("/issues/{pr}/comments", f.listComments)
		r.Post("/issues/{pr}/comments", f.createComment)
		r.Get("/issues/comments/{comment}", f.getComment)
		r.Patch("/issues/comments/{comment}", f.updateComment)
	})
}

func (f *Fake) commentPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := r.Context().Value(tokenKey).(token)            //nolint:forcetypeassert // asInstallation binds it
		in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation binds it
		f.mu.Lock()
		perms := t.perms
		if perms == nil {
			perms = f.apps[in.AppID].Permissions
		}
		level := perms["pull_requests"]
		f.mu.Unlock()
		if level != "write" && (r.Method != http.MethodGet || level != "read") {
			answer(w, http.StatusForbidden, message("Resource not accessible by integration"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func commentKey(r *http.Request) string { return deploymentRepo(r) + "/" + chi.URLParam(r, "pr") }
func commentJSON(c CommentRecord, issue string) map[string]any {
	return map[string]any{"id": c.ID, "node_id": c.NodeID, "body": c.Body, "issue_url": issue}
}

func (f *Fake) listComments(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]map[string]any, 0, len(f.comments.records[commentKey(r)]))
	for _, c := range f.comments.records[commentKey(r)] {
		items = append(items, commentJSON(c, host(r)+"/api/v3/repos/"+deploymentRepo(r)+"/issues/"+chi.URLParam(r, "pr")))
	}
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 30
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	start := min((page-1)*per, len(items))
	end := min(start+per, len(items))
	last := max(1, (len(items)+per-1)/per)
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(last))
	links := []string{"<" + host(r) + r.URL.Path + "?" + q.Encode() + ">; rel=\"last\""}
	if page < last {
		q.Set("page", strconv.Itoa(page+1))
		links = append(links, "<"+host(r)+r.URL.Path+"?"+q.Encode()+">; rel=\"next\"")
	}
	w.Header().Set("Link", strings.Join(links, ", "))
	answer(w, http.StatusOK, items[start:end])
}

func (f *Fake) commentIndex(r *http.Request) (string, int) {
	id, err := strconv.ParseInt(chi.URLParam(r, "comment"), 10, 64)
	if err != nil {
		return "", -1
	}
	prefix := deploymentRepo(r) + "/"
	for key, list := range f.comments.records {
		if strings.HasPrefix(key, prefix) {
			for i, c := range list {
				if c.ID == id {
					return key, i
				}
			}
		}
	}
	return "", -1
}

func (f *Fake) getComment(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, i := f.commentIndex(r)
	if i < 0 {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	_, pr, _ := strings.Cut(key, deploymentRepo(r)+"/")
	answer(w, http.StatusOK, commentJSON(f.comments.records[key][i], host(r)+"/api/v3/repos/"+deploymentRepo(r)+"/issues/"+pr))
}

func (f *Fake) createComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Body == "" {
		answer(w, http.StatusUnprocessableEntity, message("invalid comment"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.newComment(commentKey(r), body.Body)
	if f.comments.failCreate {
		f.comments.failCreate = false
		answer(w, http.StatusBadGateway, message("response lost"))
		return
	}
	answer(w, http.StatusCreated, commentJSON(c, host(r)+"/api/v3/repos/"+deploymentRepo(r)+"/issues/"+chi.URLParam(r, "pr")))
}

func (f *Fake) updateComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Body == "" {
		answer(w, http.StatusUnprocessableEntity, message("invalid comment"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key, i := f.commentIndex(r)
	if i < 0 {
		answer(w, http.StatusNotFound, message("Not Found"))
		return
	}
	c := &f.comments.records[key][i]
	c.Body = body.Body
	f.commentWrite(key, *c, "update")
	_, pr, _ := strings.Cut(key, deploymentRepo(r)+"/")
	answer(w, http.StatusOK, commentJSON(*c, host(r)+"/api/v3/repos/"+deploymentRepo(r)+"/issues/"+pr))
}

func (f *Fake) minimizeComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	mutation := false
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		answer(w, http.StatusBadRequest, message("invalid GraphQL request"))
		return
	}
	mutation = strings.Contains(body.Query, "minimizeComment") && strings.Contains(body.Query, "classifier:OUTDATED")
	query := strings.Contains(body.Query, "node(id:$id)") && strings.Contains(body.Query, "isMinimized") && strings.Contains(body.Query, "minimizedReason")
	if mutation == query {
		answer(w, http.StatusBadRequest, message("unsupported mutation"))
		return
	}
	in := r.Context().Value(installKey).(*Installation) //nolint:forcetypeassert // asInstallation binds it
	tok := r.Context().Value(tokenKey).(token)          //nolint:forcetypeassert // as above
	f.mu.Lock()
	defer f.mu.Unlock()
	perms := tok.perms
	if perms == nil {
		perms = f.apps[in.AppID].Permissions
	}
	for key, list := range f.comments.records {
		for i, c := range list {
			if c.NodeID == body.Variables["id"] {
				owner, rest, _ := strings.Cut(key, "/")
				repo, _, _ := strings.Cut(rest, "/")
				if in.Owner != owner || !slices.Contains(in.Repos, repo) || tok.repos != nil && !slices.Contains(tok.repos, repo) {
					answer(w, http.StatusNotFound, message("Not Found"))
					return
				}
				if perms["pull_requests"] != "write" && (!query || perms["pull_requests"] != "read") {
					answer(w, http.StatusForbidden, message("Resource not accessible by integration"))
					return
				}

				if query {
					reason := ""
					if c.Minimized {
						reason = "outdated"
					}
					answer(w, http.StatusOK, map[string]any{"data": map[string]any{"node": map[string]any{"id": c.NodeID, "isMinimized": c.Minimized, "minimizedReason": reason}}})
					return
				}
				if f.comments.minimizeAttempts == nil {
					f.comments.minimizeAttempts = map[string][]string{}
				}
				f.comments.minimizeAttempts[key] = append(f.comments.minimizeAttempts[key], c.NodeID)
				if !c.Minimized {
					f.comments.records[key][i].Minimized = true
					f.commentWrite(key, c, "minimize")
				}

				if f.comments.failMinimize {
					f.comments.failMinimize = false
					answer(w, http.StatusBadGateway, message("minimize response lost"))
					return
				}
				answer(w, http.StatusOK, map[string]any{"data": map[string]any{"minimizeComment": map[string]any{"minimizedComment": map[string]bool{"isMinimized": true}}}})
				return
			}
		}
	}
	answer(w, http.StatusNotFound, message("Not Found"))
}

// CommentMinimizeAttempts records actual native mutation attempts, separately
// from accepted state changes, so client-side recovery can be proved.
func (f *Fake) CommentMinimizeAttempts(namespace, repo, pr string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.comments.minimizeAttempts[namespace+"/"+repo+"/"+pr])
}

// FailNextCommentMinimize loses one mutation response after applying its effect.
func (f *Fake) FailNextCommentMinimize() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments.failMinimize = true
}
