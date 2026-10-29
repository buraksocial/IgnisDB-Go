// Package rustcore provides CGo bindings to the complete Rust LSM Engine.
// All storage operations are delegated to Rust - Go is a thin wrapper only.
package rustcore

/*
#cgo LDFLAGS: ${SRCDIR}/../../rust-core/target/release/libignisdb_core.a -lpthread -ldl -lm
#include <stdlib.h>
#include <stdint.h>

typedef void* EnginePtr;

EnginePtr ignis_engine_open(const uint8_t* data_dir, size_t data_dir_len, uint64_t memtable_size_limit, size_t cache_size);
int32_t ignis_engine_close(EnginePtr ptr);
int32_t ignis_engine_put(EnginePtr ptr, const uint8_t* key, size_t key_len, const uint8_t* value, size_t value_len);
int32_t ignis_engine_get(EnginePtr ptr, const uint8_t* key, size_t key_len, uint8_t** out_value, size_t* out_len);
int32_t ignis_engine_delete(EnginePtr ptr, const uint8_t* key, size_t key_len);
int32_t ignis_engine_flush(EnginePtr ptr);
int32_t ignis_engine_compact(EnginePtr ptr);
size_t ignis_engine_memtable_len(EnginePtr ptr);
uint64_t ignis_engine_memtable_bytes(EnginePtr ptr);
size_t ignis_engine_sstable_count(EnginePtr ptr);
uint64_t ignis_engine_stats_writes(EnginePtr ptr);
uint64_t ignis_engine_stats_reads(EnginePtr ptr);
uint64_t ignis_engine_stats_cache_hits(EnginePtr ptr);
uint64_t ignis_engine_stats_cache_misses(EnginePtr ptr);
void ignis_free_bytes(uint8_t* ptr, size_t len);
*/
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"
)

// Engine wraps the Rust LSM Engine via CGo.
type Engine struct {
	ptr C.EnginePtr
}

// Open opens or creates an LSM engine.
func Open(dataDir string, memtableSizeLimit uint64, cacheSize int) (*Engine, error) {
	dirBytes := []byte(dataDir)
	ptr := C.ignis_engine_open(
		(*C.uint8_t)(unsafe.Pointer(&dirBytes[0])),
		C.size_t(len(dirBytes)),
		C.uint64_t(memtableSizeLimit),
		C.size_t(cacheSize),
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to open engine at %s", dataDir)
	}
	e := &Engine{ptr: ptr}
	runtime.SetFinalizer(e, func(e *Engine) { e.Close() })
	return e, nil
}

// Close shuts down the engine.
func (e *Engine) Close() error {
	if e.ptr == nil {
		return nil
	}
	ret := C.ignis_engine_close(e.ptr)
	e.ptr = nil
	if ret != 0 {
		return fmt.Errorf("engine close failed")
	}
	return nil
}

// Put writes a key-value pair.
func (e *Engine) Put(key, value []byte) error {
	if e.ptr == nil {
		return fmt.Errorf("engine closed")
	}
	var kp, vp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	if len(value) > 0 {
		vp = (*C.uint8_t)(unsafe.Pointer(&value[0]))
	}
	ret := C.ignis_engine_put(e.ptr, kp, C.size_t(len(key)), vp, C.size_t(len(value)))
	if ret != 0 {
		return fmt.Errorf("put failed")
	}
	return nil
}

// Get retrieves a value by key.
func (e *Engine) Get(key []byte) ([]byte, bool) {
	if e.ptr == nil {
		return nil, false
	}
	var kp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	var outVal *C.uint8_t
	var outLen C.size_t
	found := C.ignis_engine_get(e.ptr, kp, C.size_t(len(key)), &outVal, &outLen)
	if found == 0 || outVal == nil {
		return nil, false
	}
	result := C.GoBytes(unsafe.Pointer(outVal), C.int(outLen))
	C.ignis_free_bytes(outVal, outLen)
	return result, true
}

// Delete removes a key.
func (e *Engine) Delete(key []byte) error {
	if e.ptr == nil {
		return fmt.Errorf("engine closed")
	}
	var kp *C.uint8_t
	if len(key) > 0 {
		kp = (*C.uint8_t)(unsafe.Pointer(&key[0]))
	}
	ret := C.ignis_engine_delete(e.ptr, kp, C.size_t(len(key)))
	if ret != 0 {
		return fmt.Errorf("delete failed")
	}
	return nil
}

// Flush forces a memtable flush to SSTable.
func (e *Engine) Flush() error {
	if e.ptr == nil {
		return fmt.Errorf("engine closed")
	}
	ret := C.ignis_engine_flush(e.ptr)
	if ret != 0 {
		return fmt.Errorf("flush failed")
	}
	return nil
}

// Compact triggers SSTable compaction.
func (e *Engine) Compact() error {
	if e.ptr == nil {
		return fmt.Errorf("engine closed")
	}
	ret := C.ignis_engine_compact(e.ptr)
	if ret != 0 {
		return fmt.Errorf("compact failed")
	}
	return nil
}

// MemtableLen returns the number of entries in the memtable.
func (e *Engine) MemtableLen() int {
	if e.ptr == nil {
		return 0
	}
	return int(C.ignis_engine_memtable_len(e.ptr))
}

// MemtableBytes returns the approximate memtable size in bytes.
func (e *Engine) MemtableBytes() uint64 {
	if e.ptr == nil {
		return 0
	}
	return uint64(C.ignis_engine_memtable_bytes(e.ptr))
}

// SSTableCount returns the number of SSTables.
func (e *Engine) SSTableCount() int {
	if e.ptr == nil {
		return 0
	}
	return int(C.ignis_engine_sstable_count(e.ptr))
}

// StatsWrites returns total write count.
func (e *Engine) StatsWrites() uint64 {
	if e.ptr == nil {
		return 0
	}
	return uint64(C.ignis_engine_stats_writes(e.ptr))
}

// StatsReads returns total read count.
func (e *Engine) StatsReads() uint64 {
	if e.ptr == nil {
		return 0
	}
	return uint64(C.ignis_engine_stats_reads(e.ptr))
}

// StatsCacheHits returns cache hit count.
func (e *Engine) StatsCacheHits() uint64 {
	if e.ptr == nil {
		return 0
	}
	return uint64(C.ignis_engine_stats_cache_hits(e.ptr))
}

// StatsCacheMisses returns cache miss count.
func (e *Engine) StatsCacheMisses() uint64 {
	if e.ptr == nil {
		return 0
	}
	return uint64(C.ignis_engine_stats_cache_misses(e.ptr))
}
