// Package bloom implements a space-efficient probabilistic data structure
// for set membership testing using xxhash3-style fast hashing.
// False positives are possible, but false negatives are not.
package bloom

import (
	"encoding/binary"
	"math"
	"unsafe"
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

// xxhash3-inspired fast hash (simplified version for bloom filter)
// Uses multiply-shift hashing which is much faster than SHA-256
const (
	prime1 = 0x9E3779B185EBCA87
	prime2 = 0xC2B2AE3D27D4EB4F
	prime3 = 0x165667B19E3779F9
)

func fastHash(data []byte, seed uint64) uint64 {
	var h uint64 = seed ^ prime1
	length := len(data)

	// Process 8 bytes at a time
	if length >= 8 {
		for i := 0; i+8 <= length; i += 8 {
			k := binary.LittleEndian.Uint64(data[i : i+8])
			k *= prime2
			k = (k << 31) | (k >> 33)
			k *= prime3
			h ^= k
			h = ((h << 27) | (h >> 37)) * prime1
		}
	}

	// Process remaining bytes
	remaining := data[length&^7:]
	for i := 0; i < len(remaining); i++ {
		h ^= uint64(remaining[i]) * prime3
		h = ((h << 11) | (h >> 53)) * prime1
	}

	// Final avalanche
	h ^= uint64(length)
	h ^= h >> 33
	h *= prime2
	h ^= h >> 29
	h *= prime3
	h ^= h >> 32

	return h
}

// hashPair generates two independent hashes from one computation
// using the double-hashing technique: h(i) = h1 + i*h2
func (bf *Filter) hashPair(key []byte) (uint64, uint64) {
	h1 := fastHash(key, 0)
	h2 := fastHash(key, h1)
	return h1, h2
}

// Add adds a key to the Bloom filter.
func (bf *Filter) Add(key []byte) {
	h1, h2 := bf.hashPair(key)
	size := uint64(bf.Size)

	for i := 0; i < bf.HashCount; i++ {
		pos := (h1 + uint64(i)*h2) % size
		bf.Bits[pos/8] |= 1 << (pos % 8)
	}
	bf.Count++
}

// MightContain checks if a key might be in the set. False positives are possible.
func (bf *Filter) MightContain(key []byte) bool {
	h1, h2 := bf.hashPair(key)
	size := uint64(bf.Size)

	for i := 0; i < bf.HashCount; i++ {
		pos := (h1 + uint64(i)*h2) % size
		if bf.Bits[pos/8]&(1<<(pos%8)) == 0 {
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

// Ensure unsafe is used (for potential future optimizations)
var _ = unsafe.Pointer(nil)
