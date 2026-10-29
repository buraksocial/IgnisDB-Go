// Package rustcore provides CGo bindings to the Rust IgnisDB core library.
// This enables high-performance lock-free operations from Go.
package rustcore

/*
#cgo LDFLAGS: ${SRCDIR}/../../rust-core/target/release/libignisdb_core.a -lpthread -ldl -lm
#include <stdlib.h>
#include <stdint.h>

// SortedMap
typedef void* SortedMapPtr;
SortedMapPtr ignis_sorted_map_new();
void ignis_sorted_map_free(SortedMapPtr ptr);
void ignis_sorted_map_put(SortedMapPtr ptr, const uint8_t* key, size_t key_len, const uint8_t* value, size_t value_len);
int32_t ignis_sorted_map_get(SortedMapPtr ptr, const uint8_t* key, size_t key_len, uint8_t** out_value, size_t* out_len);
int32_t ignis_sorted_map_delete(SortedMapPtr ptr, const uint8_t* key, size_t key_len);
size_t ignis_sorted_map_len(SortedMapPtr ptr);
void ignis_free_bytes(uint8_t* ptr, size_t len);

// BloomFilter
typedef void* BloomPtr;
BloomPtr ignis_bloom_new(size_t expected_items, double fp_rate);
void ignis_bloom_free(BloomPtr ptr);
void ignis_bloom_add(BloomPtr ptr, const uint8_t* key, size_t key_len);
int32_t ignis_bloom_might_contain(BloomPtr ptr, const uint8_t* key, size_t key_len);
*/
import "C"
import (
	"runtime"
	"unsafe"
)

// ============================================================
// SortedMap - Lock-free sorted map backed by Rust BTreeMap
// ============================================================

// SortedMap is a high-performance sorted map backed by Rust.
type SortedMap struct {
	ptr C.SortedMapPtr
}

// NewSortedMap creates a new Rust-backed sorted map.
func NewSortedMap() *SortedMap {
	sm := &SortedMap{
		ptr: C.ignis_sorted_map_new(),
	}
	runtime.SetFinalizer(sm, func(sm *SortedMap) {
		sm.Free()
	})
	return sm
}

// Free releases the Rust-side memory.
func (sm *SortedMap) Free() {
	if sm.ptr != nil {
		C.ignis_sorted_map_free(sm.ptr)
		sm.ptr = nil
	}
}

// Put inserts or updates a key-value pair.
func (sm *SortedMap) Put(key, value []byte) {
	if sm.ptr == nil {
		return
	}
	var kp, vp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	if len(value) > 0 {
		vp = (*C.uint8_t)(unsafe.Pointer(&value[0]))
	}
	C.ignis_sorted_map_put(sm.ptr, kp, C.size_t(len(key)), vp, C.size_t(len(value)))
}

// Get retrieves a value by key.
func (sm *SortedMap) Get(key []byte) ([]byte, bool) {
	if sm.ptr == nil {
		return nil, false
	}
	var kp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	var outVal *C.uint8_t
	var outLen C.size_t
	found := C.ignis_sorted_map_get(sm.ptr, kp, C.size_t(len(key)), &outVal, &outLen)
	if found == 0 || outVal == nil {
		return nil, false
	}
	// Copy to Go-managed memory
	result := C.GoBytes(unsafe.Pointer(outVal), C.int(outLen))
	C.ignis_free_bytes(outVal, outLen)
	return result, true
}

// Delete removes a key.
func (sm *SortedMap) Delete(key []byte) bool {
	if sm.ptr == nil {
		return false
	}
	var kp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	return C.ignis_sorted_map_delete(sm.ptr, kp, C.size_t(len(key))) != 0
}

// Len returns the number of entries.
func (sm *SortedMap) Len() int {
	if sm.ptr == nil {
		return 0
	}
	return int(C.ignis_sorted_map_len(sm.ptr))
}

// ============================================================
// BloomFilter - Fast bloom filter backed by Rust
// ============================================================

// RustBloom is a high-performance bloom filter backed by Rust.
type RustBloom struct {
	ptr C.BloomPtr
}

// NewRustBloom creates a new Rust-backed bloom filter.
func NewRustBloom(expectedItems int, fpRate float64) *RustBloom {
	bf := &RustBloom{
		ptr: C.ignis_bloom_new(C.size_t(expectedItems), C.double(fpRate)),
	}
	runtime.SetFinalizer(bf, func(bf *RustBloom) {
		bf.Free()
	})
	return bf
}

// Free releases the Rust-side memory.
func (bf *RustBloom) Free() {
	if bf.ptr != nil {
		C.ignis_bloom_free(bf.ptr)
		bf.ptr = nil
	}
}

// Add adds a key to the bloom filter.
func (bf *RustBloom) Add(key []byte) {
	if bf.ptr == nil {
		return
	}
	var kp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	C.ignis_bloom_add(bf.ptr, kp, C.size_t(len(key)))
}

// MightContain checks if a key might be in the set.
func (bf *RustBloom) MightContain(key []byte) bool {
	if bf.ptr == nil {
		return true
	}
	var kp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	return C.ignis_bloom_might_contain(bf.ptr, kp, C.size_t(len(key))) != 0
}
