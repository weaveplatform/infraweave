package azuredevopsfake

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
	record.ThreadID = record.ID
	record.ID = 1
	f.comments.records[key] = append(f.comments.records[key], record)
	f.commentWrite(key, record, "create")
	return record
}

func (f *Fake) commentWrite(key string, record CommentRecord, action string) {
	f.comments.writes[key] = append(f.comments.writes[key], CommentWrite{ID: record.ID, Action: action, Body: record.Body})
}

// ScopeThreads permits native pull-request discussion writes.
const ScopeThreads = "vso.threads_full"

func (f *Fake) commentRoutes(r chi.Router) {
	r.Route("/pullRequests/{pr}/threads", func(r chi.Router) {
		r.Use(f.commentPermission)
		r.Get("/", f.listComments)
		r.Post("/", f.createComment)
		r.Get("/{thread}/comments/{comment}", f.getComment)
		r.Patch("/{thread}/comments/{comment}", f.updateComment)
	})
}

func (f *Fake) commentPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !tokenOf(r).holds(ScopeThreads) {
			answer(w, http.StatusForbidden, problem("missing threads scope"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func commentKey(r *http.Request) string {
	return param(r, "project") + "/" + param(r, "repository") + "/" + param(r, "pr")
}

func commentJSON(c CommentRecord) map[string]any {
	return map[string]any{"id": c.ID, "content": c.Body, "commentType": "text", "isDeleted": false}
}

func threadJSON(c CommentRecord) map[string]any {
	return map[string]any{"id": c.ThreadID, "comments": []map[string]any{commentJSON(c)}, "publishedDate": "2026-01-01T00:00:00Z", "isDeleted": false}
}

func (f *Fake) listComments(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]map[string]any, 0, len(f.comments.records[commentKey(r)]))
	for _, c := range f.comments.records[commentKey(r)] {
		items = append(items, threadJSON(c))
	}
	answer(w, http.StatusOK, map[string]any{"count": len(items), "value": items})
}

func (f *Fake) commentIndex(r *http.Request) (string, int) {
	key := commentKey(r)
	id, err := strconv.ParseInt(param(r, "comment"), 10, 64)
	if err != nil {
		return key, -1
	}
	thread, err := strconv.ParseInt(param(r, "thread"), 10, 64)
	if err != nil {
		return key, -1
	}
	return key, slices.IndexFunc(f.comments.records[key], func(c CommentRecord) bool { return c.ID == id && c.ThreadID == thread })
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
		Comments []struct {
			Content string `json:"content"`
			Parent  int    `json:"parentCommentId"`
			Type    int    `json:"commentType"`
		} `json:"comments"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Comments) != 1 || body.Comments[0].Content == "" || body.Comments[0].Parent != 0 || body.Comments[0].Type != 1 {
		answer(w, http.StatusBadRequest, problem("invalid thread"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.newComment(commentKey(r), body.Comments[0].Content)
	if f.comments.failCreate {
		f.comments.failCreate = false
		answer(w, http.StatusBadGateway, problem("response lost"))
		return
	}
	answer(w, http.StatusCreated, threadJSON(c))
}

func (f *Fake) updateComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Content == "" {
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
	c.Body = body.Content
	f.commentWrite(key, *c, "update")
	answer(w, http.StatusOK, commentJSON(*c))
}
