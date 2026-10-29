# Architecture: Hybrid Go + Rust

🌐 **English** · [Türkçe](architecture_TR.md)

This document explains why IgnisDB-Go is designed the way it is, how data flows through the
system, and which hot-path optimizations are applied.

---

## 1. Design Philosophy: "Partial Hybrid"

Instead of pure Go or pure Rust, a **hybrid** was chosen. The reason came out of measurements:

| Approach tried | Result | Why kept / dropped |
|----------------|--------|--------------------|
| **Pure Python v2** | ~50K ops/s write | Reference port; slow |
| **Pure Go (port)** | ~8.5M ops/s, 0 alloc | ✅ Selected (hot path in Go) |
| **Go + Full Rust Core** | write ~1M ops/s | ❌ CGo boundary on every op (~1µs/op) slowed the hot path |
| **Go + Rust Hybrid (Partial)** | **write ~7.6M, read ~5.6M, ~1 alloc/op** | ✅ **Selected** |

### Why did the Full Rust Core slow down?

In the full-Rust design, **every** Put/Get was a CGo function call. The CGo boundary itself
is not just ~30–50 ns; combined with Rust→Go data copying (`Vec<u8>` → `C.GoBytes`) and the
goroutine/thread handoff, it added ~1µs per call. The read hot path went from 65 ns (pure Go)
to 454 ns (full Rust).

### The hybrid rule

> **Move to Rust only the primitives that are clearly faster than Go on their own; pull the
> boundary out of the hot path, behind a single FFI gate.**

Ported to Rust:
- **SortedMap** (a `BTreeMap` for the MemTable — no Go GC / pointer-chain cost)
- **Bloom filter hash** (xxhash-like fast hash)

Kept in Go (zero / near-zero allocation):
- MemTable SkipList (for flush + iteration), WAL, SSTable (mmap), LRU Cache, sorted set,
  full-text search, RESP server, orchestration.

---

## 2. Layers

```
┌───────────────────────────────────────────────────────────┐
│  pkg/client          RESP client (Go)                      │
├───────────────────────────────────────────────────────────┤
│  internal/server     RESP protocol parser + command router │  Go
│                      (in-memory lists/hashes/sets/ttl)     │
├───────────────────────────────────────────────────────────┤
│  internal/engine     LSM orchestration (Lock, flush/compact)│  Go
├───────────────────────────────────────────────────────────┤
│  internal/rustcore   CGo FFI bridge                        │  Go↔Rust single gate
├───────────────────────────────────────────────────────────┤
│  rust-core/src/lib   SortedMap (BTreeMap) + Fast Bloom      │  Rust (staticlib)
├───────────────────────────────────────────────────────────┤
│  internal/memtable · wal · sstable · cache · bloom ·        │  Go
│  sortedset · search                                          │
└───────────────────────────────────────────────────────────┘
```

`rust-core` compiles as a `staticlib` (`libignisdb_core.a`) with LTO + opt-level=3. The Go side
links its absolute path via `#cgo LDFLAGS`. If Rust init fails, the engine falls back gracefully
to pure Go with `useRust=false` (`recover()` + fallback).

---

## 3. Tiered Storage (3 tiers)

| Tier | Medium | Component | Access |
|------|--------|-----------|--------|
| **Hot** | RAM | LRU Cache (default 64 MB) | pointer deref, O(1) |
| **Hot/Warm** | RAM | MemTable (16 MB) + immutables | SkipList lookup + Rust SortedMap |
| **Warm** | RAM (OS) | SSTable **mmap** pages | page cache, zero-copy |
| **Cold** | Disk | SSTable files + WAL | blocking disk I/O |

### Read path (`Get`)

```
Get(key)
  1. LRU Cache hit?            →  RETURN (hottest)
  2. Rust SortedMap hit?       →  RETURN + populate cache
  3. Go SkipList (active) hit? →  RETURN + populate cache
  4. Immutable memtables (new→old)?  →  RETURN
  5. SSTables (new→old):
        MightContain(key)?  ← Bloom (skip disk entirely if negative)
        SSTable.Get(key)    ← sparse index + mmap block scan
  6. absent → (nil, false)
```

The **Bloom filter** skips disk/mmap access entirely on negative reads — the key to read-cold
performance.

### Write path (`Put`)

```
Put(key,value)
  1. WAL.Append          →  buffered (256 KB bufio) + sync.Pool buffer + CRC32
  2. MemTable.Put        →  Go SkipList (near-zero alloc via node pool)
  3. cache NOT updated   →  write-back policy (see §4)
  4. memtableSize ≥ limit →  swap + hand to flush (background)
```

Flush (background `flushLoop`): MemTable → sorted → `sstable.Create` (data blocks + sparse
index + bloom + footer) → append to the SSTable list → remove from immutables →
`WAL.Truncate`.

Compaction (background `compactLoop`): when the L0 count crosses the threshold, `sstable.Merge`
combines all SSTables into a single sorted file and drops tombstones.

---

## 4. Hot-Path Optimizations (near-zero alloc)

How the measured **~1 alloc/op** at the engine level was achieved:

| Optimization | File | Win |
|--------------|------|-----|
| **SkipList node `sync.Pool`** | `memtable/skiplist.go` | node allocation → reuse on put/iter (1024-node pre-warm) |
| **WAL buffer `sync.Pool`** | `wal/wal.go` | the entry buffer isn't re-allocated on every write (256-buffer pre-warm) |
| **xxhash-like fast hash** | `bloom/bloom.go` | ~10x faster than SHA-256, allocation-free hashing |
| **Write-back cache** | `cache/lru.go` | cache isn't written on Put → 2+ alloc/op removed |
| **`appendInt` (bench)** | `benchmarks/` | allocation-free int→byte instead of `strconv.Itoa` |
| **mmap zero-copy** | `sstable/sstable.go` | file contents aren't copied into a user buffer |
| **Rust SortedMap** | `rust-core/lib.rs` | BTreeMap write order with no GC pressure |

### Write-back cache choice

`Put` deliberately does not update the cache. The cache fills **on read**:

```go
// Get miss → memtable/SSTable hit → cache.Put  (single point)
// Put      → only WAL + MemTable                (never touches the cache)
```

This removes map key/value copying + LRU node allocation from the hot write path. Consistency
is maintained because `Get` always reads the freshest source (memtable → SSTable) and `Delete`
evicts from the cache.

---

## 5. Component Responsibilities

| Package | Responsibility | Main type |
|---------|----------------|-----------|
| `engine` | orchestration, locking, flush/compact triggering, stats | `LSMEngine`, `Config` |
| `memtable` | sorted in-memory write buffer (flush + iteration) | `SkipList` |
| `rustcore` | CGo bridge, delegates hot primitives to Rust | `SortedMap`, `NewBloomFilter` |
| `sstable` | persistent sorted file + mmap + sparse index + bloom | `SSTable` |
| `wal` | crash-safe append-only log (buffered, CRC32) | `WAL`, `Entry` |
| `cache` | fixed-size LRU hot cache | `LRUCache` |
| `bloom` | probabilistic membership (fast negative reject) | `BloomFilter` |
| `sortedset` | scored set (ZSET) | `SortedSet` |
| `search` | inverted index + TF score | `InvertedIndex`, `SearchResult` |
| `server` | RESP parse + command routing + in-memory types | `Server` |

---

## 6. Concurrency

- `LSMEngine.mu` (`sync.RWMutex`): protects the memtable/SSTable/sortedset structure.
  Reads use `RLock` in parallel; write/swap/flush use `Lock`.
- `flushMu`: serializes flushes.
- `compacting` (`atomic int32` + CAS): prevents concurrent compaction.
- Counters (`stats.*`) are protected with `atomic.Add/Load`.
- The `flushCh`/`compactCh` channels ensure the hot path never **blocks** on flush — if the
  channel is full it escapes to `go doFlushMemtable` instead of dropping.

---

## 7. Crash Recovery Flow

```
Open(dir):
  loadSSTables()   ← sstable_*.db (sorted) + sequence re-init
  wal.OpenFile()   ← append mode
  replayWAL()      ← re-apply all WalPut/WalDelete into the MemTable (CRC-checked)
```

If a WAL CRC32 mismatches, replay stops at that point (partial write = corrupted record). After
flush, `WAL.Truncate` (rename-to-.old + new empty file) shrinks the log.

---

## Next Steps

- Configuration values → [configuration.md](configuration.md)
- Production operations (flush/compact/stats) → [operations.md](operations.md)
