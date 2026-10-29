// Package memtable implements a SkipList-based sorted in-memory data structure
// for the LSM engine's MemTable. Provides O(log n) insert, delete, and search.
package memtable

import (
	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
)

const (
	// MaxLevel is the maximum number of levels in the skip list.
	MaxLevel = 16
	// P is the probability factor for level generation.
	P = 0.25
)

// Node represents a node in the skip list.
type Node struct {
	Key     []byte
	Value   []byte
	Forward [MaxLevel + 1]*Node
}

// SkipList is a sorted in-memory data structure for MemTable.
type SkipList struct {
	header *Node
	level  int
	size   int64
	count  int64
	mu     sync.RWMutex
}

// New creates a new empty SkipList.
func New() *SkipList {
	return &SkipList{
		header: &Node{},
		level:  0,
	}
}

// randomLevel generates a random level for a new node.
func randomLevel() int {
	lvl := 0
	for rand.Float64() < P && lvl < MaxLevel {
		lvl++
	}
	return lvl
}

// Put inserts or updates a key-value pair.
func (sl *SkipList) Put(key, value []byte) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := [MaxLevel + 1]*Node{}
	current := sl.header

	// Find position at each level
	for i := sl.level; i >= 0; i-- {
		for current.Forward[i] != nil && bytes.Compare(current.Forward[i].Key, key) < 0 {
			current = current.Forward[i]
		}
		update[i] = current
	}

	current = current.Forward[0]

	// Update existing key
	if current != nil && bytes.Equal(current.Key, key) {
		oldLen := len(current.Value)
		current.Value = value
		atomic.AddInt64(&sl.size, int64(len(value)-oldLen))
		return
	}

	// Insert new node
	newLevel := randomLevel()
	if newLevel > sl.level {
		for i := sl.level + 1; i <= newLevel; i++ {
			update[i] = sl.header
		}
		sl.level = newLevel
	}

	newNode := &Node{
		Key:   key,
		Value: value,
	}
	for i := 0; i <= newLevel; i++ {
		newNode.Forward[i] = update[i].Forward[i]
		update[i].Forward[i] = newNode
	}

	atomic.AddInt64(&sl.count, 1)
	atomic.AddInt64(&sl.size, int64(len(key)+len(value)))
}

// Get retrieves a value by key. Returns nil, false if not found.
func (sl *SkipList) Get(key []byte) ([]byte, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	current := sl.header
	for i := sl.level; i >= 0; i-- {
		for current.Forward[i] != nil && bytes.Compare(current.Forward[i].Key, key) < 0 {
			current = current.Forward[i]
		}
	}

	current = current.Forward[0]
	if current != nil && bytes.Equal(current.Key, key) {
		return current.Value, true
	}
	return nil, false
}

// Delete removes a key. Returns true if the key was found and deleted.
func (sl *SkipList) Delete(key []byte) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	update := [MaxLevel + 1]*Node{}
	current := sl.header

	for i := sl.level; i >= 0; i-- {
		for current.Forward[i] != nil && bytes.Compare(current.Forward[i].Key, key) < 0 {
			current = current.Forward[i]
		}
		update[i] = current
	}

	current = current.Forward[0]

	if current != nil && bytes.Equal(current.Key, key) {
		for i := 0; i <= sl.level; i++ {
			if update[i].Forward[i] != current {
				break
			}
			update[i].Forward[i] = current.Forward[i]
		}

		for sl.level > 0 && sl.header.Forward[sl.level] == nil {
			sl.level--
		}

		atomic.AddInt64(&sl.count, -1)
		atomic.AddInt64(&sl.size, -int64(len(key)+len(current.Value)))
		return true
	}
	return false
}

// Contains checks if a key exists.
func (sl *SkipList) Contains(key []byte) bool {
	_, ok := sl.Get(key)
	return ok
}

// Len returns the number of entries.
func (sl *SkipList) Len() int64 {
	return atomic.LoadInt64(&sl.count)
}

// Size returns approximate memory usage in bytes.
func (sl *SkipList) Size() int64 {
	return atomic.LoadInt64(&sl.size)
}

// Item represents a key-value pair.
type Item struct {
	Key   []byte
	Value []byte
}

// Items returns all key-value pairs in sorted order.
func (sl *SkipList) Items() []Item {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	items := make([]Item, 0, sl.count)
	current := sl.header.Forward[0]
	for current != nil {
		items = append(items, Item{Key: current.Key, Value: current.Value})
		current = current.Forward[0]
	}
	return items
}

// Keys returns all keys in sorted order.
func (sl *SkipList) Keys() [][]byte {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	keys := make([][]byte, 0, sl.count)
	current := sl.header.Forward[0]
	for current != nil {
		keys = append(keys, current.Key)
		current = current.Forward[0]
	}
	return keys
}

// RangeIter iterates over keys in range [startKey, endKey].
// Pass nil for startKey to begin from the first element.
// Pass nil for endKey to iterate to the last element.
func (sl *SkipList) RangeIter(startKey, endKey []byte) []Item {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var items []Item
	current := sl.header

	// Find starting position
	if startKey != nil {
		for i := sl.level; i >= 0; i-- {
			for current.Forward[i] != nil && bytes.Compare(current.Forward[i].Key, startKey) < 0 {
				current = current.Forward[i]
			}
		}
		current = current.Forward[0]
	} else {
		current = current.Forward[0]
	}

	// Iterate until end
	for current != nil {
		if endKey != nil && bytes.Compare(current.Key, endKey) > 0 {
			break
		}
		items = append(items, Item{Key: current.Key, Value: current.Value})
		current = current.Forward[0]
	}
	return items
}

// Clear removes all entries.
func (sl *SkipList) Clear() {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	sl.header = &Node{}
	sl.level = 0
	atomic.StoreInt64(&sl.count, 0)
	atomic.StoreInt64(&sl.size, 0)
}

// Iterator provides sequential access to skip list entries.
type Iterator struct {
	current *Node
}

// NewIterator creates an iterator starting at the first element.
func (sl *SkipList) NewIterator() *Iterator {
	sl.mu.RLock()
	return &Iterator{current: sl.header.Forward[0]}
}

// Next advances the iterator. Returns false when exhausted.
func (it *Iterator) Next() bool {
	if it.current == nil {
		return false
	}
	it.current = it.current.Forward[0]
	return it.current != nil
}

// Key returns the current key.
func (it *Iterator) Key() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.Key
}

// Value returns the current value.
func (it *Iterator) Value() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.Value
}
