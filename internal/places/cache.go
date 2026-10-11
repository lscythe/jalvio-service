package places

import (
	"container/list"
	"sync"
	"time"
)

// cache is a size-bounded LRU whose entries also expire after ttl.
type cache[V any] struct {
	mu    sync.Mutex
	size  int
	ttl   time.Duration
	order *list.List // most recently used first
	items map[string]*list.Element
}

type cacheEntry[V any] struct {
	key     string
	value   V
	expires time.Time
}

func newCache[V any](size int, ttl time.Duration) *cache[V] {
	return &cache[V]{size: size, ttl: ttl, order: list.New(), items: map[string]*list.Element{}}
}

func (c *cache[V]) get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	e := el.Value.(*cacheEntry[V])
	if !time.Now().Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.value, true
}

func (c *cache[V]) put(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := &cacheEntry[V]{key: key, value: value, expires: time.Now().Add(c.ttl)}
	if el, ok := c.items[key]; ok {
		el.Value = e
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(e)
	if c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry[V]).key)
	}
}
