// Package engine provides a thin Go wrapper around the Rust LSM Engine.
// All storage operations are delegated to Rust via CGo FFI.
package engine

import (
	"github.com/buraksocial/IgnisDB-Go/internal/rustcore"
)

const (
	DefaultMemtableSize = 16 * 1024 * 1024 // 16 MB
	DefaultCacheSize    = 64 * 1024 * 1024  // 64 MB
)

// Config holds engine configuration.
type Config struct {
	DataDir      string
	MemtableSize int64
	CacheSize    int
}

// LSMEngine wraps the Rust LSM engine.
type LSMEngine struct {
	rust *rustcore.Engine
}

// Open opens or creates an LSM engine.
func Open(cfg Config) (*LSMEngine, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.MemtableSize == 0 {
		cfg.MemtableSize = DefaultMemtableSize
	}
	if cfg.CacheSize == 0 {
		cfg.CacheSize = DefaultCacheSize
	}

	rust, err := rustcore.Open(cfg.DataDir, uint64(cfg.MemtableSize), cfg.CacheSize)
	if err != nil {
		return nil, err
	}
	return &LSMEngine{rust: rust}, nil
}

// Close shuts down the engine.
func (e *LSMEngine) Close() error {
	return e.rust.Close()
}

// Put writes a key-value pair.
func (e *LSMEngine) Put(key, value []byte) error {
	return e.rust.Put(key, value)
}

// Get retrieves a value by key.
func (e *LSMEngine) Get(key []byte) ([]byte, bool) {
	return e.rust.Get(key)
}

// Delete removes a key.
func (e *LSMEngine) Delete(key []byte) bool {
	return e.rust.Delete(key) == nil
}

// Flush forces a memtable flush.
func (e *LSMEngine) Flush() {
	e.rust.Flush()
}

// Compact triggers compaction.
func (e *LSMEngine) Compact() {
	e.rust.Compact()
}

// MemtableSize returns approximate memtable size in bytes.
func (e *LSMEngine) MemtableSize() int64 {
	return int64(e.rust.MemtableBytes())
}

// SSTableCount returns the number of SSTables.
func (e *LSMEngine) SSTableCount() int {
	return e.rust.SSTableCount()
}

// GetStats returns engine statistics.
func (e *LSMEngine) GetStats() map[string]interface{} {
	reads := e.rust.StatsReads()
	hits := e.rust.StatsCacheHits()
	misses := e.rust.StatsCacheMisses()
	var hitRate float64
	if reads > 0 {
		hitRate = float64(hits) / float64(reads) * 100
	}
	return map[string]interface{}{
		"writes":       e.rust.StatsWrites(),
		"reads":        reads,
		"cache_hits":   hits,
		"cache_misses": misses,
		"hit_rate_pct": hitRate,
		"memtable_entries": e.rust.MemtableLen(),
		"memtable_bytes":   e.rust.MemtableBytes(),
		"sstable_count":    e.rust.SSTableCount(),
		"rust_core_active": true,
	}
}
