// Package sortedset implements a score-sorted set of unique members,
// compatible with Redis ZSET operations.
package sortedset

import (
	"encoding/binary"
	"math"
	"sort"
	"sync"
)

// Member represents a member with its score.
type Member struct {
	Score  float64
	Member []byte
}

// SortedSet is a score-sorted set of unique members.
type SortedSet struct {
	members map[string]float64 // member -> score
	sorted  []Member           // sorted by (score, member)
	mu      sync.RWMutex
}

// New creates a new empty SortedSet.
func New() *SortedSet {
	return &SortedSet{
		members: make(map[string]float64),
	}
}

// ZAdd adds or updates a member with given score.
// Returns 1 if new member added, 0 if updated.
func (ss *SortedSet) ZAdd(score float64, member []byte) int {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	m := string(member)
	if oldScore, exists := ss.members[m]; exists {
		// Remove old entry from sorted list
		ss.removeSorted(oldScore, member)
		ss.members[m] = score
		ss.insertSorted(score, member)
		return 0
	}

	ss.members[m] = score
	ss.insertSorted(score, member)
	return 1
}

// ZRem removes a member. Returns 1 if removed, 0 if not found.
func (ss *SortedSet) ZRem(member []byte) int {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	m := string(member)
	score, exists := ss.members[m]
	if !exists {
		return 0
	}

	delete(ss.members, m)
	ss.removeSorted(score, member)
	return 1
}

// ZScore returns the score of a member, or NaN if not found.
func (ss *SortedSet) ZScore(member []byte) (float64, bool) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	score, ok := ss.members[string(member)]
	return score, ok
}

// ZCard returns the number of members.
func (ss *SortedSet) ZCard() int {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return len(ss.members)
}

// ZRange returns members by rank (index), inclusive.
// Negative indices count from end (-1 = last).
func (ss *SortedSet) ZRange(start, stop int) [][]byte {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	n := len(ss.sorted)
	if n == 0 {
		return nil
	}

	// Normalize negative indices
	if start < 0 {
		start = max(0, n+start)
	}
	if stop < 0 {
		stop = n + stop
	}

	// Clamp to valid range
	start = max(0, min(start, n-1))
	stop = max(0, min(stop, n-1))

	result := make([][]byte, 0, stop-start+1)
	for i := start; i <= stop; i++ {
		member := make([]byte, len(ss.sorted[i].Member))
		copy(member, ss.sorted[i].Member)
		result = append(result, member)
	}
	return result
}

// ZRevRange returns members by rank in reverse order.
func (ss *SortedSet) ZRevRange(start, stop int) [][]byte {
	result := ss.ZRange(start, stop)
	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// ZRangeByScore returns members with scores in [minScore, maxScore].
func (ss *SortedSet) ZRangeByScore(minScore, maxScore float64, offset, limit int) [][]byte {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// Find range using binary search
	left := sort.Search(len(ss.sorted), func(i int) bool {
		return ss.sorted[i].Score >= minScore
	})
	right := sort.Search(len(ss.sorted), func(i int) bool {
		return ss.sorted[i].Score > maxScore
	})

	results := make([][]byte, 0)
	for i := left; i < right; i++ {
		member := make([]byte, len(ss.sorted[i].Member))
		copy(member, ss.sorted[i].Member)
		results = append(results, member)
	}

	if limit > 0 {
		end := offset + limit
		if end > len(results) {
			end = len(results)
		}
		if offset > len(results) {
			return nil
		}
		return results[offset:end]
	}
	if offset > len(results) {
		return nil
	}
	return results[offset:]
}

// ZRevRangeByScore returns members in reverse score order within range.
func (ss *SortedSet) ZRevRangeByScore(maxScore, minScore float64, offset, limit int) [][]byte {
	result := ss.ZRangeByScore(minScore, maxScore, 0, -1)
	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	if limit > 0 {
		end := offset + limit
		if end > len(result) {
			end = len(result)
		}
		if offset > len(result) {
			return nil
		}
		return result[offset:end]
	}
	if offset > len(result) {
		return nil
	}
	return result[offset:]
}

// ZCount counts members with scores in [minScore, maxScore].
func (ss *SortedSet) ZCount(minScore, maxScore float64) int {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	left := sort.Search(len(ss.sorted), func(i int) bool {
		return ss.sorted[i].Score >= minScore
	})
	right := sort.Search(len(ss.sorted), func(i int) bool {
		return ss.sorted[i].Score > maxScore
	})
	return right - left
}

// ZRank returns the rank (0-based index) of a member by score ascending.
func (ss *SortedSet) ZRank(member []byte) (int, bool) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	score, ok := ss.members[string(member)]
	if !ok {
		return 0, false
	}

	idx := sort.Search(len(ss.sorted), func(i int) bool {
		if ss.sorted[i].Score > score {
			return true
		}
		if ss.sorted[i].Score == score {
			return string(ss.sorted[i].Member) >= string(member)
		}
		return false
	})

	if idx < len(ss.sorted) && string(ss.sorted[idx].Member) == string(member) {
		return idx, true
	}
	return 0, false
}

// ZRevRank returns the rank by score descending.
func (ss *SortedSet) ZRevRank(member []byte) (int, bool) {
	rank, ok := ss.ZRank(member)
	if !ok {
		return 0, false
	}
	return len(ss.sorted) - 1 - rank, true
}

// Serialize serializes the sorted set for persistence.
func (ss *SortedSet) Serialize() []byte {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// count(4) + for each: score(8) + member_len(4) + member
	size := 4
	for _, m := range ss.sorted {
		size += 8 + 4 + len(m.Member)
	}

	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(ss.sorted)))
	pos := 4
	for _, m := range ss.sorted {
		binary.LittleEndian.PutUint64(buf[pos:pos+8], math.Float64bits(m.Score))
		pos += 8
		binary.LittleEndian.PutUint32(buf[pos:pos+4], uint32(len(m.Member)))
		pos += 4
		copy(buf[pos:], m.Member)
		pos += len(m.Member)
	}
	return buf
}

// Deserialize deserializes a sorted set from bytes.
func Deserialize(data []byte) *SortedSet {
	ss := New()
	if len(data) < 4 {
		return ss
	}

	count := int(binary.LittleEndian.Uint32(data[0:4]))
	pos := 4

	for i := 0; i < count; i++ {
		if pos+12 > len(data) {
			break
		}
		score := math.Float64frombits(binary.LittleEndian.Uint64(data[pos : pos+8]))
		pos += 8
		memberLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if pos+memberLen > len(data) {
			break
		}
		member := make([]byte, memberLen)
		copy(member, data[pos:pos+memberLen])
		pos += memberLen
		ss.ZAdd(score, member)
	}
	return ss
}

// Clear removes all members.
func (ss *SortedSet) Clear() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.members = make(map[string]float64)
	ss.sorted = nil
}

// insertSorted inserts a member into the sorted slice maintaining order.
func (ss *SortedSet) insertSorted(score float64, member []byte) {
	m := Member{Score: score, Member: member}
	idx := sort.Search(len(ss.sorted), func(i int) bool {
		if ss.sorted[i].Score > score {
			return true
		}
		if ss.sorted[i].Score == score {
			return string(ss.sorted[i].Member) >= string(member)
		}
		return false
	})

	// Insert at idx
	ss.sorted = append(ss.sorted, Member{})
	copy(ss.sorted[idx+1:], ss.sorted[idx:])
	ss.sorted[idx] = m
}

// removeSorted removes a member from the sorted slice.
func (ss *SortedSet) removeSorted(score float64, member []byte) {
	idx := sort.Search(len(ss.sorted), func(i int) bool {
		if ss.sorted[i].Score > score {
			return true
		}
		if ss.sorted[i].Score == score {
			return string(ss.sorted[i].Member) >= string(member)
		}
		return false
	})

	if idx < len(ss.sorted) && string(ss.sorted[idx].Member) == string(member) {
		ss.sorted = append(ss.sorted[:idx], ss.sorted[idx+1:]...)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
