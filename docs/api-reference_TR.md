# API Referansı

🌐 [English](api-reference.md) · **Türkçe**

Tam Go tip imzaları. Motor ve sunucu `internal/` altında (yalnızca aynı modülden import);
istemci `pkg/client` altında (public).

---

## `engine` — `internal/engine`

LSM-Tree orkestrasyon katmanı. Çoğu kullanıcı yalnızca bu pakete dokunur.

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

// Yaşam döngüsü
func Open(cfg Config) (*LSMEngine, error)
func (e *LSMEngine) Close() error

// Temel KV
func (e *LSMEngine) Put(key, value []byte) error
func (e *LSMEngine) Get(key []byte) ([]byte, bool)
func (e *LSMEngine) Delete(key []byte) bool

// Flush / boyut
func (e *LSMEngine) Flush()
func (e *LSMEngine) MemtableSize() int64
func (e *LSMEngine) SSTableCount() int

// Sorted set (ZSET)
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

// Tanılama
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

> Yalnızca RESP `SET/GET/DEL/PING` sarar. Diğer komut aileleri için gömülü motoru kullan veya
> ham RESP gönder.

---

## `memtable` — `internal/memtable`

Aktif MemTable olarak kullanılan eşzamanlı skip list (flush + iterasyon).

```go
type SkipList struct { /* ... */ }
type Item struct { Key, Value []byte }

func New() *SkipList
func (sl *SkipList) Put(key, value []byte)
func (sl *SkipList) Get(key []byte) ([]byte, bool)
func (sl *SkipList) Delete(key []byte) bool
func (sl *SkipList) Contains(key []byte) bool
func (sl *SkipList) Len() int64
func (sl *SkipList) Size() int64            // yaklaşık bayt
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

mmap ile desteklenen kalıcı, immutable sıralı dosya (run).

```go
type SSTable struct { Path string /* ... */ }

func Create(mt *memtable.SkipList, dataDir string, sequence int64) (*SSTable, error)
func Open(path string) (*SSTable, error)
func (sst *SSTable) MightContain(key []byte) bool   // bloom kontrolü
func (sst *SSTable) Get(key []byte) ([]byte, bool)  // sparse index + mmap taraması
func (sst *SSTable) EntryCount() int
func (sst *SSTable) FileSize() int64
func (sst *SSTable) Close() error
func Merge(sstables []*SSTable, dataDir string, sequence int64) (*SSTable, error) // compaction
```

---

## `wal` — `internal/wal`

Buffered, CRC32 doğrulamalı write-ahead log.

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

Sabit boyutlu LRU hot cache (write-back).

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

Olasılıksal üyelik filtresi (Go tarafı; `rustcore` içinde Rust ikizi var).

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

ZSET komutlarının arkasındaki skorlu küme.

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

Terim-frekansı skorlamalı inverted index.

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

Rust statik kütüphanesine (`libignisdb_core.a`) CGo köprüsü.

```go
type SortedMap struct { /* opaque Rust *mut */ }

func NewSortedMap() *SortedMap
func (sm *SortedMap) Put(key, value []byte)
func (sm *SortedMap) Get(key []byte) ([]byte, bool)
func (sm *SortedMap) Delete(key []byte) bool
func (sm *SortedMap) Len() int
func (sm *SortedMap) Free()   // Rust tahsisini serbest bırak

type RustBloom struct { /* opaque Rust *mut */ }

func NewRustBloom(expectedItems int, fpRate float64) *RustBloom
func (bf *RustBloom) Add(key []byte)
func (bf *RustBloom) MightContain(key []byte) bool
func (bf *RustBloom) Free()
```

> **FFI güvenliği:** her `*SortedMap` / `*RustBloom` sonunda `Free()` çağırmalı. Motor bunu
> flush ve `Close()`'ta yapar. Yedek olarak runtime finalizer da kurulur.

---

## `server` — `internal/server`

RESP sunucusu. Programatik kullanım:

```go
type Server struct { /* ... */ }

func New(eng *engine.LSMEngine, addr string) *Server
func (s *Server) ListenAndServe() error
func (s *Server) Stop()
```

## İlgili

- Bağlam içinde davranış → [embedded-usage_TR.md](embedded-usage_TR.md)
- Tel protokolü → [server-protocol_TR.md](server-protocol_TR.md)
