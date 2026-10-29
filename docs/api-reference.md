# API Reference

🌐 **English** · [Türkçe](api-reference_TR.md)

Complete Go type signatures. The engine and server live under `internal/` (same-module
import only); the client is under `pkg/client` (public).

---

## `engine` — `internal/engine`

The LSM-Tree orchestration layer. Most users only touch this package.

```go
type Config struct {
    DataDir             string
    MemtableSize        int64
    CacheSize           int
    CompactionThreshold int
}

type Stats struct {
    Writes, Reads, CacheHits, CacheMisses, SSTableReads, Compactions int64
}

type LSMEngine struct { /* ... */ }

// Lifecycle
func Open(cfg Config) (*LSMEngine, error)
func (e *LSMEngine) Close() error

// Core KV
func (e *LSMEngine) Put(key, value []byte) error
func (e *LSMEngine) Get(key []byte) ([]byte, bool)
func (e *LSMEngine) Delete(key []byte) bool

// Flush / sizing
func (e *LSMEngine) Flush()
func (e *LSMEngine) MemtableSize() int64
func (e *LSMEngine) SSTableCount() int

// Sorted sets (ZSET)
func (e *LSMEngine) ZAdd(key []byte, score float64, member []byte) int
func (e *LSMEngine) ZRem(key, member []byte) int
func (e *LSMEngine) ZScore(key, member []byte) (float64, bool)
func (e *LSMEngine) ZRange(key []byte, start, stop int) [][]byte
func (e *LSMEngine) ZRevRange(key []byte, start, stop int) [][]byte
func (e *LSMEngine) ZRangeByScore(key []byte, min, max float64, offset, limit int) [][]byte
func (e *LSMEngine) ZRevRangeByScore(key []byte, max, min float64, offset, limit int) [][]byte
func (e *LSMEngine) ZCard(key []byte) int

// Full-text search
func (e *LSMEngine) FTAdd(docID, text string)
func (e *LSMEngine) FTSearch(query string, limit int) []search.SearchResult
func (e *LSMEngine) FTRemove(docID string)

// Diagnostics
func (e *LSMEngine) GetStats() map[string]interface{}

const (
    DefaultMemtableSize        = 16 * 1024 * 1024
    DefaultCacheSize           = 64 * 1024 * 1024
    DefaultCompactionThreshold = 4
)
```

---

## `client` — `pkg/client`

```go
type Client struct { /* ... */ }

func Connect(addr string) (*Client, error)
func (c *Client) Close() error
func (c *Client) Set(key, value string) error
func (c *Client) Get(key string) (string, error)
func (c *Client) Del(keys ...string) (int64, error)
func (c *Client) Ping() (string, error)
```

> Wraps RESP `SET/GET/DEL/PING` only. For other command families, use the embedded engine or
> send raw RESP yourself.

---

## `memtable` — `internal/memtable`

Concurrent skip list used as the active MemTable (flush + iteration).

```go
type SkipList struct { /* ... */ }
type Item struct { Key, Value []byte }

func New() *SkipList
func (sl *SkipList) Put(key, value []byte)
func (sl *SkipList) Get(key []byte) ([]byte, bool)
func (sl *SkipList) Delete(key []byte) bool
func (sl *SkipList) Contains(key []byte) bool
func (sl *SkipList) Len() int64
func (sl *SkipList) Size() int64            // approximate bytes
func (sl *SkipList) Keys() [][]byte
func (sl *SkipList) RangeIter(startKey, endKey []byte) []Item
func (sl *SkipList) Clear()
func (sl *SkipList) NewIterator() *Iterator

type Iterator struct { /* ... */ }
func (it *Iterator) Next() bool
func (it *Iterator) Key() []byte
func (it *Iterator) Value() []byte
```

---

## `sstable` — `internal/sstable`

Persistent, immutable sorted run backed by mmap.

```go
type SSTable struct { Path string /* ... */ }

func Create(mt *memtable.SkipList, dataDir string, sequence int64) (*SSTable, error)
func Open(path string) (*SSTable, error)
func (sst *SSTable) MightContain(key []byte) bool   // bloom check
func (sst *SSTable) Get(key []byte) ([]byte, bool)  // sparse index + mmap scan
func (sst *SSTable) EntryCount() int
func (sst *SSTable) FileSize() int64
func (sst *SSTable) Close() error
func Merge(sstables []*SSTable, dataDir string, sequence int64) (*SSTable, error) // compaction
```

---

## `wal` — `internal/wal`

Buffered, CRC32-checked write-ahead log.

```go
type Entry struct { Type uint8; Key, Value []byte }
type WAL struct { /* ... */ }

const (
    WalPut    uint8 = 1
    WalDelete uint8 = 2
)

func Open(dataDir string) *WAL
func (w *WAL) OpenFile() error
func (w *WAL) Append(key, value []byte, entryType uint8) error
func (w *WAL) Sync() error
func (w *WAL) Replay() ([]Entry, error)
func (w *WAL) Truncate() error
func (w *WAL) FileSize() int64
func (w *WAL) Close() error
```

---

## `cache` — `internal/cache`

Fixed-size LRU hot cache (write-back).

```go
type LRUCache struct { /* ... */ }

func NewLRU(maxBytes int) *LRUCache
func (c *LRUCache) Get(key []byte) ([]byte, bool)
func (c *LRUCache) Put(key, value []byte)
func (c *LRUCache) Delete(key []byte) bool
func (c *LRUCache) Clear()
func (c *LRUCache) Len() int
func (c *LRUCache) CurrentSize() int
func (c *LRUCache) Stats() map[string]interface{}  // hits, misses, hit_rate_pct, hot_cache_bytes
```

---

## `bloom` — `internal/bloom`

Probabilistic membership filter (Go-side; a Rust twin exists in `rustcore`).

```go
type Filter struct { /* ... */ }

func New(expectedItems int, fpRate float64) *Filter
func NewFromKeys(keys [][]byte, fpRate float64) *Filter
func (bf *Filter) Add(key []byte)
func (bf *Filter) MightContain(key []byte) bool
func (bf *Filter) Serialize() []byte
func Deserialize(data []byte) *Filter
func (bf *Filter) MemoryUsage() int
```

---

## `sortedset` — `internal/sortedset`

Scored set backing the ZSET commands.

```go
type SortedSet struct { /* ... */ }
type Member struct { Score float64; Member []byte }

func New() *SortedSet
func (ss *SortedSet) ZAdd(score float64, member []byte) int
func (ss *SortedSet) ZRem(member []byte) int
func (ss *SortedSet) ZScore(member []byte) (float64, bool)
func (ss *SortedSet) ZCard() int
func (ss *SortedSet) ZRange(start, stop int) [][]byte
func (ss *SortedSet) ZRevRange(start, stop int) [][]byte
func (ss *SortedSet) ZRangeByScore(min, max float64, offset, limit int) [][]byte
func (ss *SortedSet) ZRevRangeByScore(max, min float64, offset, limit int) [][]byte
func (ss *SortedSet) ZCount(min, max float64) int
func (ss *SortedSet) ZRank(member []byte) (int, bool)
func (ss *SortedSet) ZRevRank(member []byte) (int, bool)
func (ss *SortedSet) Serialize() []byte
func Deserialize(data []byte) *SortedSet
func (ss *SortedSet) Clear()
```

---

## `search` — `internal/search`

Inverted index with term-frequency scoring.

```go
type InvertedIndex struct { DocCount int /* ... */ }
type SearchResult struct { DocID string; Score float64 }

func NewInvertedIndex() *InvertedIndex
func (idx *InvertedIndex) AddDocument(docID, text string)
func (idx *InvertedIndex) RemoveDocument(docID string)
func (idx *InvertedIndex) Search(query string, limit int) []SearchResult
func (idx *InvertedIndex) GetTerms(docID string) []string
func (idx *InvertedIndex) Stats() map[string]interface{}
func (idx *InvertedIndex) Clear()
```

---

## `rustcore` — `internal/rustcore`

CGo bridge to the Rust static library (`libignisdb_core.a`).

```go
type SortedMap struct { /* opaque Rust *mut */ }

func NewSortedMap() *SortedMap
func (sm *SortedMap) Put(key, value []byte)
func (sm *SortedMap) Get(key []byte) ([]byte, bool)
func (sm *SortedMap) Delete(key []byte) bool
func (sm *SortedMap) Len() int
func (sm *SortedMap) Free()   // release the Rust allocation

type RustBloom struct { /* opaque Rust *mut */ }

func NewRustBloom(expectedItems int, fpRate float64) *RustBloom
func (bf *RustBloom) Add(key []byte)
func (bf *RustBloom) MightContain(key []byte) bool
func (bf *RustBloom) Free()
```

> **FFI safety:** every `*SortedMap` / `*RustBloom` must eventually call `Free()`. The engine
> does this on flush and `Close()`. A runtime finalizer is also set as a backstop.

---

## `server` — `internal/server`

RESP server. Programmatic use:

```go
type Server struct { /* ... */ }

func New(eng *engine.LSMEngine, addr string) *Server
func (s *Server) ListenAndServe() error
func (s *Server) Stop()
```

## Related

- Behavior in context → [embedded-usage.md](embedded-usage.md)
- Wire protocol → [server-protocol.md](server-protocol.md)
