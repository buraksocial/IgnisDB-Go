// Package cache implements a fixed-size LRU hot cache for frequently accessed keys.
// Optimized with reduced allocations and efficient eviction.
package cache

import (
	"container/list"
	"sync"
	"time"
)

// entry represents a cache entry with access tracking.
type entry struct {
	key         []byte
	value       []byte
	accessCount int
	lastAccess  time.Time
}

// LRUCache is a thread-safe LRU cache with a fixed byte budget.
type LRUCache struct {
	maxBytes    int
	currentSize int
	items       map[string]*list.Element
	order       *list.List
	mu          sync.Mutex

	// Statistics
	Hits   int64
	Misses int64
}

// NewLRU creates a new LRU cache with the given max byte size.
func NewLRU(maxBytes int) *LRUCache {
	return &LRUCache{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Get retrieves a value from the cache. Updates LRU order on hit.
func (c *LRUCache) Get(key []byte) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	k := string(key)
	if elem, ok := c.items[k]; ok {
		c.order.MoveToFront(elem)
		e := elem.Value.(*entry)
		e.accessCount++
		e.lastAccess = time.Now()
		c.Hits++
		return e.value, true
	}
	c.Misses++
	return nil, false
}

// Put adds or updates a value in the cache with LRU eviction.
func (c *LRUCache) Put(key, value []byte) {
	size := len(key) + len(value)

	// Don't cache items larger than 10% of cache
	if size > c.maxBytes/10 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	k := string(key)

	// Update existing key
	if elem, ok := c.items[k]; ok {
		c.order.MoveToFront(elem)
		e := elem.Value.(*entry)
		c.currentSize -= len(e.key) + len(e.value)
		e.key = key
		e.value = value
		e.accessCount++
		e.lastAccess = time.Now()
		c.currentSize += size
		return
	}

	// Evict until we have space
	for c.currentSize+size > c.maxBytes && c.order.Len() > 0 {
		c.evictColdest()
	}

	// Add new entry
	e := &entry{
		key:         key,
		value:       value,
		accessCount: 1,
		lastAccess:  time.Now(),
	}
	elem := c.order.PushFront(e)
	c.items[k] = elem
	c.currentSize += size
}

// Delete removes a key from the cache.
func (c *LRUCache) Delete(key []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	k := string(key)
	if elem, ok := c.items[k]; ok {
		e := elem.Value.(*entry)
		c.currentSize -= len(e.key) + len(e.value)
		c.order.Remove(elem)
		delete(c.items, k)
		return true
	}
	return false
}

// evictColdest removes the least recently used item.
func (c *LRUCache) evictColdest() {
	back := c.order.Back()
	if back == nil {
		return
	}
	e := back.Value.(*entry)
	c.currentSize -= len(e.key) + len(e.value)
	c.order.Remove(back)
	delete(c.items, string(e.key))
}

// Clear removes all cached data.
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.order.Init()
	c.currentSize = 0
}

// Len returns the number of cached entries.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// CurrentSize returns the current cache size in bytes.
func (c *LRUCache) CurrentSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentSize
}

// Stats returns cache statistics.
func (c *LRUCache) Stats() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	totalRequests := c.Hits + c.Misses
	var hitRate float64
	if totalRequests > 0 {
		hitRate = float64(c.Hits) / float64(totalRequests) * 100
	}

	utilization := 0.0
	if c.maxBytes > 0 {
		utilization = float64(c.currentSize) / float64(c.maxBytes) * 100
	}

	return map[string]interface{}{
		"hot_cache_entries": len(c.items),
		"hot_cache_bytes":   c.currentSize,
		"max_hot_bytes":     c.maxBytes,
		"utilization_pct":   utilization,
		"hits":              c.Hits,
		"misses":            c.Misses,
		"hit_rate_pct":      hitRate,
	}
}
