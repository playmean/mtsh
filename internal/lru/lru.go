package lru

import (
	"container/list"
	"sync"
	"time"
)

type entry struct {
	key    string
	expiry time.Time
}

type Cache struct {
	mu   sync.Mutex
	cap  int
	ttl  time.Duration
	ll   *list.List
	idx  map[string]*list.Element
	nowF func() time.Time
}

func New(capacity int, ttl time.Duration) *Cache {
	if capacity <= 0 {
		capacity = 1024
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Cache{
		cap:  capacity,
		ttl:  ttl,
		ll:   list.New(),
		idx:  map[string]*list.Element{},
		nowF: time.Now,
	}
}

// Seen returns true if key is already present and not expired; otherwise records it and returns false.
func (c *Cache) Seen(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.nowF()

	if el, ok := c.idx[key]; ok {
		en := el.Value.(*entry)
		if now.Before(en.expiry) {
			c.ll.MoveToFront(el)
			return true
		}
		// expired -> remove and treat as new
		c.ll.Remove(el)
		delete(c.idx, key)
	}

	// insert
	en := &entry{key: key, expiry: now.Add(c.ttl)}
	el := c.ll.PushFront(en)
	c.idx[key] = el

	// evict tail
	for c.ll.Len() > c.cap {
		tail := c.ll.Back()
		if tail == nil {
			break
		}
		en2 := tail.Value.(*entry)
		delete(c.idx, en2.key)
		c.ll.Remove(tail)
	}

	// opportunistic cleanup of expired tails
	for {
		tail := c.ll.Back()
		if tail == nil {
			break
		}
		en2 := tail.Value.(*entry)
		if now.Before(en2.expiry) {
			break
		}
		delete(c.idx, en2.key)
		c.ll.Remove(tail)
	}

	return false
}
