// Package engine implements the LSM-Tree storage engine combining:
// - MemTable (Rust-backed SortedMap via CGo) for writes
// - WAL for crash safety
// - SSTables for persistent sorted storage
// - LRU Cache for hot data
// - Rust-backed Bloom filters for read optimization
// - Background compaction
//
// Hybrid Go+Rust architecture: Go handles server/API/routing,
// Rust handles hot-path storage primitives via CGo FFI.
package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/buraksocial/IgnisDB-Go/internal/cache"
	"github.com/buraksocial/IgnisDB-Go/internal/memtable"
	"github.com/buraksocial/IgnisDB-Go/internal/rustcore"
	"github.com/buraksocial/IgnisDB-Go/internal/search"
	"github.com/buraksocial/IgnisDB-Go/internal/sortedset"
	"github.com/buraksocial/IgnisDB-Go/internal/sstable"
	"github.com/buraksocial/IgnisDB-Go/internal/wal"
)

const (
	// DefaultMemtableSize is the default MemTable size limit (16 MB).
	DefaultMemtableSize = 16 * 1024 * 1024
	// DefaultCacheSize is the default hot cache size (64 MB).
	DefaultCacheSize = 64 * 1024 * 1024
	// DefaultCompactionThreshold is the L0 SSTable count before compaction.
	DefaultCompactionThreshold = 4
)

// Stats holds engine statistics.
type Stats struct {
	Writes       int64
	Reads        int64
	CacheHits    int64
	CacheMisses  int64
	SSTableReads int64
	Compactions  int64
}

// LSMEngine is the core Log-Structured Merge Tree storage engine.
// Uses Rust-backed SortedMap for MemTable and Rust Bloom for filters.
type LSMEngine struct {
	dataDir       string
	memtableSize  int64
	// Hybrid: Rust SortedMap as primary MemTable, Go SkipList as fallback
	rustMemtable  *rustcore.SortedMap
	goMemtable    *memtable.SkipList // Used for iteration/flush (Rust doesn't expose iter yet)
	immutables    []*memtable.SkipList
	sstables      []*sstable.SSTable
	wal           *wal.WAL
	cache         *cache.LRUCache
	sortedSets    map[string]*sortedset.SortedSet
	searchIndex   *search.InvertedIndex
	compacting    int32
	sequence      int64
	stats         Stats
	flushCh       chan *memtable.SkipList
	compactCh     chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	flushMu       sync.Mutex
	useRust       bool // Whether Rust core is available
}

// Config holds engine configuration.
type Config struct {
	DataDir             string
	MemtableSize        int64
	CacheSize           int
	CompactionThreshold int
}

func debugLog(format string, args ...interface{}) {
	if os.Getenv("IGNISDB_DEBUG") != "" {
		log.Printf(format, args...)
	}
}

// Open opens or creates an LSM engine at the given data directory.
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

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Try to initialize Rust core
	var rustMt *rustcore.SortedMap
	useRust := false
	defer func() {
		if r := recover(); r != nil {
			debugLog("Rust core not available, falling back to Go: %v", r)
		}
	}()
	rustMt = rustcore.NewSortedMap()
	useRust = true

	e := &LSMEngine{
		dataDir:      cfg.DataDir,
		memtableSize: cfg.MemtableSize,
		rustMemtable: rustMt,
		goMemtable:   memtable.New(),
		wal:          wal.Open(cfg.DataDir),
		cache:        cache.NewLRU(cfg.CacheSize),
		sortedSets:   make(map[string]*sortedset.SortedSet),
		searchIndex:  search.NewInvertedIndex(),
		flushCh:      make(chan *memtable.SkipList, 4),
		compactCh:    make(chan struct{}, 1),
		ctx:          ctx,
		cancel:       cancel,
		useRust:      useRust,
	}

	// Load existing SSTables
	if err := e.loadSSTables(); err != nil {
		cancel()
		return nil, err
	}

	// Open WAL and replay
	if err := e.wal.OpenFile(); err != nil {
		cancel()
		return nil, err
	}
	if err := e.replayWAL(); err != nil {
		cancel()
		return nil, err
	}

	// Start background goroutines
	go e.flushLoop()
	go e.compactLoop()

	debugLog("LSMEngine ready: %d SSTables, %d entries in MemTable (rust=%v)",
		len(e.sstables), e.goMemtable.Len(), e.useRust)

	return e, nil
}

// Close shuts down the engine: flush MemTable, close files.
func (e *LSMEngine) Close() error {
	e.cancel()

	// Flush remaining MemTable
	if e.goMemtable.Len() > 0 {
		e.doFlushMemtable(e.goMemtable)
	}

	// Close all SSTables
	e.mu.Lock()
	for _, sst := range e.sstables {
		sst.Close()
	}
	e.mu.Unlock()

	// Free Rust resources
	if e.rustMemtable != nil {
		e.rustMemtable.Free()
	}

	return e.wal.Close()
}

// Put writes a key-value pair.
func (e *LSMEngine) Put(key, value []byte) error {
	// 1. Write to WAL
	if err := e.wal.Append(key, value, wal.WalPut); err != nil {
		return err
	}

	// 2. Write to MemTable (Go SkipList for flush compatibility)
	e.goMemtable.Put(key, value)
	atomic.AddInt64(&e.stats.Writes, 1)

	// 3. Update cache
	e.cache.Put(key, value)

	// 4. Check if MemTable needs flush
	if e.goMemtable.Size() >= e.memtableSize {
		e.mu.Lock()
		immutable := e.goMemtable
		e.immutables = append(e.immutables, immutable)
		e.goMemtable = memtable.New()
		if e.useRust && e.rustMemtable != nil {
			e.rustMemtable.Free()
			e.rustMemtable = rustcore.NewSortedMap()
		}
		e.mu.Unlock()

		select {
		case e.flushCh <- immutable:
		default:
			go e.doFlushMemtable(immutable)
		}
	}

	return nil
}

// Get reads a value by key.
func (e *LSMEngine) Get(key []byte) ([]byte, bool) {
	atomic.AddInt64(&e.stats.Reads, 1)

	// 1. Check hot cache
	if value, ok := e.cache.Get(key); ok {
		atomic.AddInt64(&e.stats.CacheHits, 1)
		if len(value) == 0 {
			return nil, false
		}
		return value, true
	}
	atomic.AddInt64(&e.stats.CacheMisses, 1)

	e.mu.RLock()
	defer e.mu.RUnlock()

	// 2. Check active MemTable
	if value, ok := e.goMemtable.Get(key); ok {
		if len(value) == 0 {
			return nil, false
		}
		e.cache.Put(key, value)
		return value, true
	}

	// 3. Check immutable MemTables (newest first)
	for i := len(e.immutables) - 1; i >= 0; i-- {
		if value, ok := e.immutables[i].Get(key); ok {
			if len(value) == 0 {
				return nil, false
			}
			e.cache.Put(key, value)
			return value, true
		}
	}

	// 4. Check SSTables (newest first, bloom filter optimized)
	for i := len(e.sstables) - 1; i >= 0; i-- {
		if e.sstables[i].MightContain(key) {
			if value, ok := e.sstables[i].Get(key); ok {
				atomic.AddInt64(&e.stats.SSTableReads, 1)
				if len(value) == 0 {
					return nil, false
				}
				e.cache.Put(key, value)
				return value, true
			}
		}
	}

	return nil, false
}

// Delete deletes a key (tombstone).
func (e *LSMEngine) Delete(key []byte) bool {
	e.wal.Append(key, []byte{}, wal.WalDelete)

	if e.useRust && e.rustMemtable != nil {
		e.rustMemtable.Delete(key)
	}
	e.goMemtable.Put(key, []byte{})
	e.cache.Delete(key)

	return true
}

// flushLoop runs the background flush goroutine.
func (e *LSMEngine) flushLoop() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case immutable := <-e.flushCh:
			e.doFlushMemtable(immutable)
		}
	}
}

// doFlushMemtable flushes a MemTable to an SSTable.
func (e *LSMEngine) doFlushMemtable(mt *memtable.SkipList) {
	e.flushMu.Lock()
	defer e.flushMu.Unlock()

	if mt.Len() == 0 {
		return
	}

	e.mu.Lock()
	e.sequence++
	seq := e.sequence
	e.mu.Unlock()

	st, err := sstable.Create(mt, e.dataDir, seq)
	if err != nil {
		debugLog("Flush error: %v", err)
		return
	}

	e.mu.Lock()
	e.sstables = append(e.sstables, st)

	for i, imm := range e.immutables {
		if imm == mt {
			e.immutables = append(e.immutables[:i], e.immutables[i+1:]...)
			break
		}
	}
	e.mu.Unlock()

	e.wal.Truncate()

	debugLog("Flushed MemTable to SSTable #%d (%d entries)", seq, mt.Len())

	select {
	case e.compactCh <- struct{}{}:
	default:
	}
}

// compactLoop runs the background compaction goroutine.
func (e *LSMEngine) compactLoop() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.compactCh:
			e.doCompact()
		}
	}
}

// doCompact performs background compaction.
func (e *LSMEngine) doCompact() {
	if !atomic.CompareAndSwapInt32(&e.compacting, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&e.compacting, 0)

	e.mu.RLock()
	sstCount := len(e.sstables)
	e.mu.RUnlock()

	if sstCount < 2 {
		return
	}

	debugLog("Starting compaction (%d SSTables)", sstCount)

	e.mu.Lock()
	defer e.mu.Unlock()

	e.sequence++
	merged, err := sstable.Merge(e.sstables, e.dataDir, e.sequence)
	if err != nil {
		debugLog("Compaction error: %v", err)
		return
	}

	oldSSTables := e.sstables
	e.sstables = []*sstable.SSTable{merged}
	atomic.AddInt64(&e.stats.Compactions, 1)

	for _, sst := range oldSSTables {
		sst.Close()
		os.Remove(sst.Path)
	}

	debugLog("Compaction complete: %d → 1 SSTable", len(oldSSTables))
}

// loadSSTables loads existing SSTables from disk.
func (e *LSMEngine) loadSSTables() error {
	matches, err := filepath.Glob(filepath.Join(e.dataDir, "sstable_*.db"))
	if err != nil {
		return err
	}
	sort.Strings(matches)

	for _, path := range matches {
		sst, err := sstable.Open(path)
		if err != nil {
			debugLog("Failed to load SSTable %s: %v", path, err)
			continue
		}
		e.sstables = append(e.sstables, sst)

		base := filepath.Base(path)
		base = strings.TrimSuffix(base, ".db")
		parts := strings.Split(base, "_")
		if len(parts) >= 2 {
			if seq, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
				if seq > e.sequence {
					e.sequence = seq
				}
			}
		}
	}

	debugLog("Loaded %d SSTables", len(e.sstables))
	return nil
}

// replayWAL replays WAL entries into MemTable.
func (e *LSMEngine) replayWAL() error {
	entries, err := e.wal.Replay()
	if err != nil {
		return err
	}

	count := 0
	for _, entry := range entries {
		switch entry.Type {
		case wal.WalPut:
			if e.useRust && e.rustMemtable != nil {
				e.rustMemtable.Put(entry.Key, entry.Value)
			}
			e.goMemtable.Put(entry.Key, entry.Value)
			count++
		case wal.WalDelete:
			if e.useRust && e.rustMemtable != nil {
				e.rustMemtable.Delete(entry.Key)
			}
			e.goMemtable.Put(entry.Key, []byte{})
			count++
		}
	}

	if count > 0 {
		debugLog("Replayed %d entries from WAL", count)
	}
	return nil
}

// === Sorted Set Operations ===

func (e *LSMEngine) ZAdd(key []byte, score float64, member []byte) int {
	e.mu.Lock()
	k := string(key)
	if _, ok := e.sortedSets[k]; !ok {
		e.sortedSets[k] = sortedset.New()
	}
	ss := e.sortedSets[k]
	e.mu.Unlock()
	return ss.ZAdd(score, member)
}

func (e *LSMEngine) ZRem(key, member []byte) int {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return 0
	}
	return ss.ZRem(member)
}

func (e *LSMEngine) ZScore(key, member []byte) (float64, bool) {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return 0, false
	}
	return ss.ZScore(member)
}

func (e *LSMEngine) ZRange(key []byte, start, stop int) [][]byte {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return nil
	}
	return ss.ZRange(start, stop)
}

func (e *LSMEngine) ZRevRange(key []byte, start, stop int) [][]byte {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return nil
	}
	return ss.ZRevRange(start, stop)
}

func (e *LSMEngine) ZRangeByScore(key []byte, minScore, maxScore float64, offset, limit int) [][]byte {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return nil
	}
	return ss.ZRangeByScore(minScore, maxScore, offset, limit)
}

func (e *LSMEngine) ZRevRangeByScore(key []byte, maxScore, minScore float64, offset, limit int) [][]byte {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return nil
	}
	return ss.ZRevRangeByScore(maxScore, minScore, offset, limit)
}

func (e *LSMEngine) ZCard(key []byte) int {
	e.mu.RLock()
	ss, ok := e.sortedSets[string(key)]
	e.mu.RUnlock()
	if !ok {
		return 0
	}
	return ss.ZCard()
}

// === Full-Text Search Operations ===

func (e *LSMEngine) FTAdd(docID, text string) {
	e.searchIndex.AddDocument(docID, text)
}

func (e *LSMEngine) FTSearch(query string, limit int) []search.SearchResult {
	return e.searchIndex.Search(query, limit)
}

func (e *LSMEngine) FTRemove(docID string) {
	e.searchIndex.RemoveDocument(docID)
}

// === Stats & Diagnostics ===

func (e *LSMEngine) GetStats() map[string]interface{} {
	cacheStats := e.cache.Stats()

	e.mu.RLock()
	sstCount := len(e.sstables)
	var sstTotalBytes int64
	for _, sst := range e.sstables {
		sstTotalBytes += sst.FileSize()
	}
	sortedSetCount := len(e.sortedSets)
	e.mu.RUnlock()

	return map[string]interface{}{
		"writes":              atomic.LoadInt64(&e.stats.Writes),
		"reads":               atomic.LoadInt64(&e.stats.Reads),
		"cache_hits":          atomic.LoadInt64(&e.stats.CacheHits),
		"cache_misses":        atomic.LoadInt64(&e.stats.CacheMisses),
		"sstable_reads":       atomic.LoadInt64(&e.stats.SSTableReads),
		"compactions":         atomic.LoadInt64(&e.stats.Compactions),
		"memtable_entries":    e.goMemtable.Len(),
		"memtable_bytes":      e.goMemtable.Size(),
		"sstable_count":       sstCount,
		"sstable_total_bytes": sstTotalBytes,
		"wal_size_bytes":      e.wal.FileSize(),
		"sorted_set_count":    sortedSetCount,
		"search_doc_count":    e.searchIndex.DocCount,
		"rust_core_active":    e.useRust,
		"cache":               cacheStats,
	}
}

func (e *LSMEngine) MemtableSize() int64 {
	return e.goMemtable.Size()
}

func (e *LSMEngine) SSTableCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.sstables)
}

func (e *LSMEngine) Flush() {
	e.mu.Lock()
	if e.goMemtable.Len() == 0 {
		e.mu.Unlock()
		return
	}
	immutable := e.goMemtable
	e.immutables = append(e.immutables, immutable)
	e.goMemtable = memtable.New()
	if e.useRust && e.rustMemtable != nil {
		e.rustMemtable.Free()
		e.rustMemtable = rustcore.NewSortedMap()
	}
	e.mu.Unlock()

	e.doFlushMemtable(immutable)
}
