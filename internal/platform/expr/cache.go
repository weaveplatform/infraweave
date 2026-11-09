package expr

import (
	"container/list"
	"sync"
)

// lru keeps the most recently used values up to a capacity, dropping the
// least recently used beyond it.
type lru[K comparable, V any] struct {
	mu    sync.Mutex
	limit int
	order *list.List
	items map[K]*list.Element
}

type lruEntry[K comparable, V any] struct {
	key K
	val V
}

func newLRU[K comparable, V any](limit int) *lru[K, V] {
	return &lru[K, V]{limit: limit, order: list.New(), items: map[K]*list.Element{}}
}

// get returns key's value, made by build on a miss. A value build fails
// to make is not kept. Two callers that miss together may both build; the
// later one's value is kept, and both are equivalent.
func (c *lru[K, V]) get(key K, build func() (V, error)) (V, error) {
	c.mu.Lock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		v := e.Value.(*lruEntry[K, V]).val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	v, err := build()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		e.Value.(*lruEntry[K, V]).val = v
		return v, nil
	}
	c.items[key] = c.order.PushFront(&lruEntry[K, V]{key, v})
	for c.order.Len() > c.limit {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*lruEntry[K, V]).key)
	}
	return v, nil
}
