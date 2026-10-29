# Embedded Usage (Go API)

🌐 **English** · [Türkçe](embedded-usage_TR.md)

This document explains how to use the IgnisDB-Go engine directly inside your own Go
application, without needing a server.

> **Module boundary:** the engine lives in an `internal/` package, which Go rules restrict to
> imports from the same module. To use it from an external project, either:
>
> ```
> require github.com/buraksocial/IgnisDB-Go v0.0.0
> replace github.com/buraksocial/IgnisDB-Go => /absolute/path/IgnisDB-Go
> ```
>
> Or run the server as the HTTP/TCP layer and use the
> [pkg/client](#the-go-client-library) client.

---

## 1. Opening the Engine

```go
import "github.com/buraksocial/IgnisDB-Go/internal/engine"

eng, err := engine.Open(engine.Config{
    DataDir:      "./data",   // default "./data"
    MemtableSize: 16 << 20,   // 16 MB   (default DefaultMemtableSize)
    CacheSize:    64 << 20,   // 64 MB   (default DefaultCacheSize)
    // CompactionThreshold: 4,        // L0 SSTable count (default 4)
})
```

| Config field | Type | Default | Meaning |
|--------------|------|---------|---------|
| `DataDir` | `string` | `./data` | SSTable + WAL directory (auto-created) |
| `MemtableSize` | `int64` | `16 MB` | MemTable is flushed once this many bytes is reached |
| `CacheSize` | `int` | `64 MB` | Hot LRU cache capacity (bytes) |
| `CompactionThreshold` | `int` | `4` | Background compaction triggers at this L0 SSTable count |

`Open`:
1. creates the directory,
2. loads existing SSTables (`sstable_*.db`),
3. opens the WAL and rebuilds the MemTable via `Replay()`,
4. starts the background `flushLoop` + `compactLoop` goroutines,
5. returns in hybrid mode (`useRust=true`) if the Rust core is available; otherwise falls back to pure Go.

**Always use `defer eng.Close()`** — it flushes the remaining MemTable, closes SSTables,
frees Rust resources, and closes the WAL.

---

## 2. Core Key-Value: Put / Get / Delete

```go
// Put: writes a key-value pair to WAL + MemTable.
if err := eng.Put([]byte("user:1"), []byte("Ada")); err != nil {
    log.Fatal(err)
}

// Get: returns (value, found). Cache → MemTable(rust) → MemTable(go) → immutables → SSTable.
val, ok := eng.Get([]byte("user:1"))
if !ok {
    fmt.Println("not found")
} else {
    fmt.Printf("user:1 => %s\n", val)
}

// Delete: writes a tombstone, evicts from cache. At the engine level this always returns true.
eng.Delete([]byte("user:1"))
_, ok = eng.Get([]byte("user:1")) // ok == false
```

### The Put/Get/Flush cycle (LSM)

```
Put(key, value)
   │
   ├── WAL.Append  (crash safety, buffered, CRC32)
   ├── MemTable.Put (Go SkipList; hot path, ~zero alloc via node pool)
   │        cache is NOT updated  ← write-back policy (saves 2+ alloc/op)
   └── MemTable.Size ≥ limit ?  → swap + hand to flush (background)
```

**Write-back cache:** a write does not update the cache. The cache is populated **on read**
only (cache miss → memtable/SSTable hit → `cache.Put`). This removes 2+ allocations from the
write hot path. Freshness is preserved because `Get` always reads the freshest source
(memtable → SSTable) and `Delete` evicts from the cache.

---

## 3. Forcing a Flush & Disk State

```go
// Flush: write the active MemTable to an SSTable immediately (e.g. before a backup).
eng.Flush()

n := eng.SSTableCount()          // active SSTable count
sz := eng.MemtableSize()         // active memtable size in bytes
fmt.Printf("%d SSTables, memtable=%d B\n", n, sz)
```

Compaction runs automatically in the background (via `sstable.Merge` when the L0 threshold is
crossed). There is **no manual public `Compact()` method**; the `compactions` counter in
`GetStats()` shows how many rounds have run.

---

## 4. Sorted Sets (ZSET)

Redis ZSET-like: `score` is a float64, members are byte slices.

```go
key := []byte("leaderboard")

added := eng.ZAdd(key, 100, []byte("alice"))   // int: 1 if new, 0 if updated
eng.ZAdd(key, 200, []byte("bob"))
eng.ZAdd(key, 150, []byte("charlie"))

card := eng.ZCard(key)                         // 3

// Score-ordered ascending range (0..-1 = all)
members := eng.ZRange(key, 0, -1)              // [alice, charlie, bob]

// Descending order
rev := eng.ZRevRange(key, 0, -1)               // [bob, charlie, alice]

// Score range + limit (offset, limit)
scoreRange := eng.ZRangeByScore(key, 100, 160, 0, 10)  // [alice, charlie]
revRange   := eng.ZRevRangeByScore(key, 160, 100, 0, -1)

// Single member score
if s, ok := eng.ZScore(key, []byte("bob")); ok {
    fmt.Printf("bob=%.0f\n", s)                 // bob=200
}

// Remove member (int: number removed)
eng.ZRem(key, []byte("charlie"))
```

| Method | Signature | Returns |
|--------|-----------|---------|
| `ZAdd` | `(key []byte, score float64, member []byte)` | `int` (1 added / 0 updated) |
| `ZRem` | `(key, member []byte)` | `int` (number removed) |
| `ZScore` | `(key, member []byte)` | `(float64, bool)` |
| `ZRange` | `(key []byte, start, stop int)` | `[][]byte` (ascending) |
| `ZRevRange` | `(key []byte, start, stop int)` | `[][]byte` (descending) |
| `ZRangeByScore` | `(key []byte, min, max float64, offset, limit int)` | `[][]byte` |
| `ZRevRangeByScore` | `(key []byte, max, min float64, offset, limit int)` | `[][]byte` |
| `ZCard` | `(key []byte)` | `int` |

`start/stop` may be negative (`-1` = last member). `limit < 0` → no limit.

---

## 5. Full-Text Search (FT)

Simple inverted index + TF (term-frequency) score:

```go
eng.FTAdd("doc1", "Go is a fast compiled language")
eng.FTAdd("doc2", "Python is great for data science")
eng.FTAdd("doc3", "Go databases are very performant")

results := eng.FTSearch("go", 10)   // limit: top N results
for _, r := range results {
    fmt.Printf("%s (score=%.4f)\n", r.DocID, r.Score)
}
// doc1 ...   doc3 ...  (both contain the term "go")

eng.FTRemove("doc1")                 // remove the document from all terms
```

`SearchResult` type:

```go
type SearchResult struct {
    DocID string
    Score float64
}
```

> The score is raw term frequency with no length normalization. The query is split into
> terms and results are ordered by descending score.

---

## 6. Statistics

```go
stats := eng.GetStats()
fmt.Printf("%+v\n", stats)
```

Returns a `map[string]interface{}`; keys:

| Key | Type | Meaning |
|-----|------|---------|
| `writes` / `reads` | `int64` | total Put / Get calls |
| `cache_hits` / `cache_misses` | `int64` | LRU cache hits / misses |
| `sstable_reads` | `int64` | records read from SSTables |
| `compactions` | `int64` | completed compaction rounds |
| `memtable_entries` / `memtable_bytes` | `int` | active MemTable contents |
| `sstable_count` / `sstable_total_bytes` | `int` / `int64` | disk storage |
| `wal_size_bytes` | `int64` | WAL file size |
| `sorted_set_count` / `search_doc_count` | `int` | secondary-structure counts |
| `rust_core_active` | `bool` | whether the hybrid Rust path is active |
| `cache` | `map[string]interface{}` | `hits`,`misses`,`hit_rate_pct`,`hot_cache_bytes` |

---

## The Go Client Library

A light client that connects to the server over TCP/RESP — use it when you don't want to embed
the engine:

```go
import "github.com/buraksocial/IgnisDB-Go/pkg/client"

c, err := client.Connect("127.0.0.1:6380")
if err != nil {
    log.Fatal(err)
}
defer c.Close()

c.Set("user:1", "Ada Lovelace")
v, _ := c.Get("user:1")        // v == "Ada Lovelace"
n, _ := c.Del("user:1")        // number of keys removed (int64)
pong, _ := c.Ping()            // "PONG"
```

| Method | Returns | Note |
|--------|---------|------|
| `Connect(addr string)` | `(*Client, error)` | TCP dial + buffered reader/writer |
| `Close()` | `error` | closes the connection |
| `Set(key, value string)` | `error` | RESP `SET` |
| `Get(key string)` | `(string, error)` | empty string if absent |
| `Del(keys ...string)` | `(int64, error)` | number removed |
| `Ping()` | `(string, error)` | `PONG` |

> The client **only wraps basic string commands** (SET/GET/DEL/PING). For ZSET / FT / list /
> hash, either embed the engine or write your own client that sends raw RESP commands (reuse
> the `sendCommand`/`readResponse` logic as a template).

---

## 7. Full Example

`examples/basic_usage.go` — opens the engine and performs Put/Get/Delete, sorted sets, and search:

```go
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func main() {
    dir := "./example_data"
    defer os.RemoveAll(dir)

    eng, err := engine.Open(engine.Config{DataDir: dir})
    if err != nil {
        log.Fatal(err)
    }
    defer eng.Close()

    // KV
    eng.Put([]byte("name"), []byte("IgnisDB-Go"))
    v, _ := eng.Get([]byte("name"))
    fmt.Printf("name = %s\n", v)

    // Sorted set
    k := []byte("board")
    eng.ZAdd(k, 100, []byte("alice"))
    eng.ZAdd(k, 200, []byte("bob"))
    eng.ZAdd(k, 150, []byte("charlie"))
    fmt.Println("ranked:", eng.ZRange(k, 0, -1)) // [alice charlie bob]

    // Search
    eng.FTAdd("d1", "Go is fast")
    eng.FTAdd("d2", "Go databases performant")
    for _, r := range eng.FTSearch("go", 10) {
        fmt.Printf("  %s %.2f\n", r.DocID, r.Score)
    }

    // Flush + stats
    eng.Flush()
    fmt.Printf("SSTables=%d\n", eng.SSTableCount())
}
```

Run it:

```bash
CGO_ENABLED=1 go run ./examples/basic_usage.go
```

---

## Next Steps

- Full command surface → [server-protocol.md](server-protocol.md)
- Why hybrid + the data path → [architecture.md](architecture.md)
- Complete list of type signatures → [api-reference.md](api-reference.md)
