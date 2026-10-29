// Package bloom implements a space-efficient probabilistic data structure
// for set membership testing. False positives are possible, but false negatives are not.
package bloom

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// Filter is a Bloom filter for probabilistic set membership testing.
type Filter struct {
	Size      int
	HashCount int
	Bits      []byte
	Count     int
}

// New creates a new Bloom filter with optimal parameters for the expected number of items.
func New(expectedItems int, fpRate float64) *Filter {
	if expectedItems <= 0 {
		expectedItems = 1
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	// Optimal size: m = -(n * ln(p)) / (ln(2)^2)
	size := int(-float64(expectedItems) * math.Log(fpRate) / (math.Log(2) * math.Log(2)))
	if size < 64 {
		size = 64
	}

	// Optimal hash count: k = (m/n) * ln(2)
	hashCount := int((float64(size) / float64(expectedItems)) * math.Log(2))
	if hashCount < 1 {
		hashCount = 1
	}

	return &Filter{
		Size:      size,
		HashCount: hashCount,
		Bits:      make([]byte, size/8+1),
		Count:     0,
	}
}

// NewFromKeys creates a Bloom filter pre-populated with the given keys.
func NewFromKeys(keys [][]byte, fpRate float64) *Filter {
	bf := New(len(keys), fpRate)
	for _, key := range keys {
		bf.Add(key)
	}
	return bf
}

// hash generates a hash value using SHA-256 with a seed.
func (bf *Filter) hash(key []byte, seed uint32) int {
	h := sha256.New()
	seedBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(seedBytes, seed)
	h.Write(seedBytes)
	h.Write(key)
	digest := h.Sum(nil)
	val := binary.LittleEndian.Uint64(digest[:8])
	return int(val % uint64(bf.Size))
}

// Add adds a key to the Bloom filter.
func (bf *Filter) Add(key []byte) {
	for i := 0; i < bf.HashCount; i++ {
		pos := bf.hash(key, uint32(i))
		bf.Bits[pos/8] |= 1 << (uint(pos) % 8)
	}
	bf.Count++
}

// MightContain checks if a key might be in the set. False positives are possible.
func (bf *Filter) MightContain(key []byte) bool {
	for i := 0; i < bf.HashCount; i++ {
		pos := bf.hash(key, uint32(i))
		if bf.Bits[pos/8]&(1<<(uint(pos)%8)) == 0 {
			return false
		}
	}
	return true
}

// Serialize serializes the Bloom filter to bytes.
// Format: size(4) + hashCount(4) + count(4) + bits
func (bf *Filter) Serialize() []byte {
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], uint32(bf.Size))
	binary.LittleEndian.PutUint32(header[4:8], uint32(bf.HashCount))
	binary.LittleEndian.PutUint32(header[8:12], uint32(bf.Count))

	result := make([]byte, 12+len(bf.Bits))
	copy(result[:12], header)
	copy(result[12:], bf.Bits)
	return result
}

// Deserialize deserializes a Bloom filter from bytes.
func Deserialize(data []byte) *Filter {
	if len(data) < 12 {
		return New(100, 0.01)
	}

	size := int(binary.LittleEndian.Uint32(data[0:4]))
	hashCount := int(binary.LittleEndian.Uint32(data[4:8]))
	count := int(binary.LittleEndian.Uint32(data[8:12]))

	bits := make([]byte, len(data)-12)
	copy(bits, data[12:])

	return &Filter{
		Size:      size,
		HashCount: hashCount,
		Bits:      bits,
		Count:     count,
	}
}

// MemoryUsage returns the memory usage in bytes.
func (bf *Filter) MemoryUsage() int {
	return len(bf.Bits) + 12
}
