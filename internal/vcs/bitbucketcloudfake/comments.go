package bitbucketcloudfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

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
	records    map[string][]CommentRecord
	writes     map[string][]CommentWrite
	failCreate bool
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

	f.comments.records[key] = append(f.comments.records[key], record)
	f.commentWrite(key, record, "create")
	return record
}

func (f *Fake) commentWrite(key string, record CommentRecord, action string) {
	f.comments.writes[key] = append(f.comments.writes[key], CommentWrite{ID: record.ID, Action: action, Body: record.Body})
}

func (f *Fake) commentRoutes(r chi.Router) {
	r.Route("/pullrequests/{pr}/comments", func(r chi.Router) {
		r.Get("/", f.listComments)
		r.Post("/", f.createComment)
		r.Get("/{comment}", f.getComment)
		r.Put("/{comment}", f.updateComment)
	})
}

func commentKey(r *http.Request) string {
	return chi.URLParam(r, "workspace") + "/" + chi.URLParam(r, "slug") + "/" + chi.URLParam(r, "pr")
}

func commentJSON(c CommentRecord) map[string]any {
	return map[string]any{"id": c.ID, "content": map[string]string{"raw": c.Body}, "deleted": false}
}

func (f *Fake) listComments(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("sort") != "-created_on" {
		answer(w, http.StatusBadRequest, problem("explicit newest order required"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	list := f.comments.records[commentKey(r)]
	for i := len(list) - 1; i >= 0; i-- {
		items = append(items, commentJSON(list[i]))
	}
	paged(w, r, items)
}

func (f *Fake) commentIndex(r *http.Request) (string, int) {
	key := commentKey(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "comment"), 10, 64)
	if err != nil {
		return key, -1
	}
	return key, slices.IndexFunc(f.comments.records[key], func(c CommentRecord) bool { return c.ID == id })
}

func (f *Fake) getComment(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, i := f.commentIndex(r)
	if i < 0 {
		answer(w, http.StatusNotFound, problem("Comment not found"))
		return
	}
	answer(w, http.StatusOK, commentJSON(f.comments.records[key][i]))
}

func (f *Fake) createComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Content.Raw == "" {
		answer(w, http.StatusBadRequest, problem("invalid comment"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.newComment(commentKey(r), body.Content.Raw)
	if f.comments.failCreate {
		f.comments.failCreate = false
		answer(w, http.StatusBadGateway, problem("response lost"))
		return
	}
	answer(w, http.StatusCreated, commentJSON(c))
}

func (f *Fake) updateComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Content.Raw == "" {
		answer(w, http.StatusBadRequest, problem("invalid comment"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key, i := f.commentIndex(r)
	if i < 0 {
		answer(w, http.StatusNotFound, problem("Comment not found"))
		return
	}
	c := &f.comments.records[key][i]
	c.Body = body.Content.Raw
	f.commentWrite(key, *c, "update")
	answer(w, http.StatusOK, commentJSON(*c))
}
