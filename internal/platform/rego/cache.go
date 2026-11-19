package rego

import (
	"container/list"
	"sync"

	oparego "github.com/open-policy-agent/opa/v1/rego"
)

type cacheKey struct {
	Body         [32]byte
	Dialect      EngineType
	Type         Type
	Preamble     string
	Capabilities [32]byte
}

type entry struct {
	key   cacheKey
	query oparego.PreparedEvalQuery
}

type queryCache struct {
	mu      sync.Mutex
	entries map[cacheKey]*list.Element
	order   *list.List
	limit   int
}

func newCache() *queryCache {
	return &queryCache{entries: map[cacheKey]*list.Element{}, order: list.New(), limit: CacheEntries}
}

func (c *queryCache) get(key cacheKey) (oparego.PreparedEvalQuery, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return oparego.PreparedEvalQuery{}, false
	}
	c.order.MoveToFront(e)
	return e.Value.(entry).query, true
}

func (c *queryCache) put(key cacheKey, q oparego.PreparedEvalQuery) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		c.order.MoveToFront(e)
		return
	}
	e := c.order.PushFront(entry{key: key, query: q})
	c.entries[key] = e
	if c.order.Len() > c.limit {
		old := c.order.Back()
		delete(c.entries, old.Value.(entry).key)
		c.order.Remove(old)
	}
}
