package acceptance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// c292QueueFields are the schedulable-run fields C292's suggestions list,
// in the recorded order.
var c292QueueFields = []string{
	"commit", "createdAt", "isDriftDetection", "isPrioritized", "position",
	"priorityPreset", "stackName", "state", "type",
}

type c292Replay struct {
	t      *testing.T
	client *sdk.Client
	// executed are the recorded pointers the replay has asserted.
	executed []string
}

// call sends one read and decodes its 200 answer into out.
func (r *c292Replay) call(method, path string, body, out any) {
	r.t.Helper()
	response, err := r.client.Do(r.t.Context(), method, path, body)
	if err != nil || response == nil || response.Status != http.StatusOK {
		r.t.Fatalf("C292 %s %s: %+v %v, want 200", method, path, response, err)
	}
	if err := json.Unmarshal(response.Body, out); err != nil {
		r.t.Fatalf("C292 %s %s: %v: %s", method, path, err, response.Body)
	}
}

// exchange marks the recorded exchange n answered, by the REST operation
// that serves its GraphQL operation.
func (r *c292Replay) exchange(n string) {
	r.executed = append(r.executed, "/exchanges/"+n+"/status", "/exchanges/"+n+"/request/operationName")
}

// check asserts one recorded pointer: got must be want.
func (r *c292Replay) check(ptr string, got, want any) {
	r.t.Helper()
	if !reflect.DeepEqual(got, want) {
		r.t.Errorf("C292 %s = %#v, want %#v", ptr, got, want)
	}
	r.executed = append(r.executed, ptr)
}

// c292Input is in as the JSON a search route's input parameter carries.
func c292Input(in any) string {
	raw, _ := json.Marshal(in)
	return url.QueryEscape(string(raw))
}

type c292Search struct {
	Edges    []json.RawMessage `json:"edges"`
	PageInfo struct {
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pageInfo"`
}

type c292Suggestions struct {
	FilteredCount int `json:"filteredCount"`
	Fields        []struct {
		Name string `json:"name"`
	} `json:"fields"`
}

type c292Pool struct {
	BusyWorkers          int    `json:"busyWorkers"`
	SchedulableRunsCount int    `json:"schedulableRunsCount"`
	UsersCount           int    `json:"usersCount"`
	Parallelism          *int64 `json:"parallelism"`
}

// publicStacks counts the replay account's stacks that select no private
// pool, the public pool's users as its home key reads them.
func (r *c292Replay) publicStacks() (ids []string) {
	r.t.Helper()
	cursor := ""
	for range 100 {
		var page struct {
			Items []struct {
				ID         string          `json:"id"`
				WorkerPool json.RawMessage `json:"workerPool"`
			} `json:"items"`
			Next *string `json:"nextCursor"`
		}
		r.call(http.MethodGet, "/api/v1/stacks?limit=100&cursor="+url.QueryEscape(cursor), nil, &page)
		for _, st := range page.Items {
			if len(st.WorkerPool) == 0 || string(st.WorkerPool) == "null" {
				ids = append(ids, st.ID)
			}
		}
		if page.Next == nil {
			slices.Sort(ids)
			return ids
		}
		cursor = *page.Next
	}
	r.t.Fatal("C292 stack inventory does not end")
	return nil
}

// pools lists the replay account's private pools.
func (r *c292Replay) pools(path string) []json.RawMessage {
	r.t.Helper()
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	r.call(http.MethodGet, path, nil, &page)
	return page.Items
}

// replayPublicPoolProjections reads each recorded exchange's REST
// equivalent: the pool overview, the public pool's queue and users with
// their suggestions, and the account's pool inventory. The public pool's
// users are the replay account's own stacks; the record's zero is the
// captured account having none. The replay server's public execution is
// switched off and its billing off, so its queue is empty and its
// parallelism null.
func replayPublicPoolProjections(t *testing.T, client *sdk.Client) []string {
	t.Helper()
	r := &c292Replay{t: t, client: client}
	users := r.publicStacks()
	before := r.pools("/api/v1/worker-pools")

	var pool c292Pool
	r.call(http.MethodGet, "/api/v1/public-worker-pool", nil, &pool)
	var pools c292Search
	r.call(http.MethodPost, "/api/v1/worker-pools/search",
		map[string]any{"orderBy": []any{map[string]any{"field": "name", "direction": "DESC"}}, "first": 50}, &pools)
	r.exchange("0")
	r.check("/exchanges/0/response/data/publicWorkerPool/busyWorkers", pool.BusyWorkers, 0)
	r.check("/exchanges/0/response/data/publicWorkerPool/schedulableRunsCount", pool.SchedulableRunsCount, 0)
	r.check("/exchanges/0/response/data/searchWorkerPools/edges", len(pools.Edges), 0)
	r.check("/exchanges/0/response/data/searchWorkerPools/pageInfo/hasNextPage", pools.PageInfo.HasNextPage, false)

	var poolSuggestions c292Suggestions
	r.call(http.MethodPost, "/api/v1/worker-pools/search/suggestions", map[string]any{}, &poolSuggestions)
	r.exchange("1")
	r.check("/exchanges/1/response/data/searchWorkerPoolsSuggestions/filteredCount", poolSuggestions.FilteredCount, 0)

	r.call(http.MethodGet, "/api/v1/public-worker-pool", nil, &pool)
	r.exchange("2")
	if pool.Parallelism != nil {
		t.Errorf("C292 public parallelism with billing off = %d, want null", *pool.Parallelism)
	}

	var queue c292Search
	r.call(http.MethodGet, "/api/v1/worker-pools/public/search-schedulable-runs?input="+c292Input(map[string]any{"first": 50}), nil, &queue)
	r.exchange("3")
	r.check("/exchanges/3/response/data/publicWorkerPool/schedulableRunsCount", pool.SchedulableRunsCount, 0)
	r.check("/exchanges/3/response/data/publicWorkerPool/usersCount", pool.UsersCount, len(users))
	r.check("/exchanges/3/response/data/publicWorkerPool/searchSchedulableRuns/edges", len(queue.Edges), 0)
	r.check("/exchanges/3/response/data/publicWorkerPool/searchSchedulableRuns/pageInfo/hasNextPage", queue.PageInfo.HasNextPage, false)

	var queueSuggestions c292Suggestions
	r.call(http.MethodGet, "/api/v1/worker-pools/public/search-schedulable-runs-suggestions?input="+c292Input(map[string]any{}), nil, &queueSuggestions)
	r.exchange("4")
	r.check("/exchanges/4/response/data/publicWorkerPool/searchSchedulableRunsSuggestions/filteredCount", queueSuggestions.FilteredCount, 0)
	for i, name := range c292QueueFields {
		got := ""
		if i < len(queueSuggestions.Fields) {
			got = queueSuggestions.Fields[i].Name
		}
		r.check("/exchanges/4/response/data/publicWorkerPool/searchSchedulableRunsSuggestions/fields/"+strconv.Itoa(i)+"/name", got, name)
	}

	r.call(http.MethodGet, "/api/v1/public-worker-pool", nil, &pool)
	r.exchange("5")

	var used c292Search
	r.call(http.MethodGet, "/api/v1/worker-pools/public/search-users?input="+c292Input(map[string]any{"first": 50}), nil, &used)
	r.exchange("6")
	r.check("/exchanges/6/response/data/publicWorkerPool/usersCount", pool.UsersCount, len(users))
	r.check("/exchanges/6/response/data/publicWorkerPool/searchUsers/edges", len(used.Edges), min(len(users), 50))
	r.check("/exchanges/6/response/data/publicWorkerPool/searchUsers/pageInfo/hasNextPage", used.PageInfo.HasNextPage, len(users) > 50)

	var usedSuggestions c292Suggestions
	r.call(http.MethodGet, "/api/v1/worker-pools/public/search-users-suggestions?input="+c292Input(map[string]any{}), nil, &usedSuggestions)
	r.exchange("7")
	r.check("/exchanges/7/response/data/publicWorkerPool/searchUsersSuggestions/filteredCount", usedSuggestions.FilteredCount, len(users))

	var image string
	var egress []string
	r.call(http.MethodGet, "/api/v1/default-public-worker-pool-runner-image", nil, &image)
	r.call(http.MethodGet, "/api/v1/outgoing-ip-addresses", nil, &egress)
	if image == "" || egress == nil || len(egress) != 0 {
		t.Errorf("C292 public runner image %q and egress %v, want the built-in image and none while public execution is off", image, egress)
	}
	r.exchange("8")
	r.check("/exchanges/8/response/data/workerPools", len(r.pools("/api/v1/worker-pools")), 0)
	r.check("/exchanges/8/response/data/attachableWorkerPoolsInSpace", len(r.pools("/api/v1/attachable-worker-pools-in-space?id=acme")), 0)

	after := r.pools("/api/v1/worker-pools")
	r.check("/readOnly", reflect.DeepEqual(before, after) && slices.Equal(users, r.publicStacks()), true)
	return r.executed
}
