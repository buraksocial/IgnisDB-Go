# IgnisDB-Go

🌐 **English** · [Türkçe](README_TR.md)

**High-performance LSM-Tree + Tiered Storage database**, written in Go.
A **hybrid (Go + Rust)** implementation of Python [IgnisDB](https://github.com/buraksocial/IgnisDB) v2,
with hot-path primitives ported to Rust.

> **~40–150x faster than Python** on writes and reads, with **near-zero allocation** on every hot operation.

| Benchmark | Python v2 | IgnisDB-Go (Hybrid) | Speedup | Allocs/op |
|-----------|-----------|---------------------|---------|-----------|
| Write       |    48,543 ops/s | **~7.59M ops/s** | **156x** | 1 |
| Read (hot)  |   125,786 ops/s | **~5.59M ops/s** |  **44x** | 1 |
| Read (cold) |    28,409 ops/s | **~4.97M ops/s** | **175x** | 1 |
| Mixed       |   108,696 ops/s | **~4.06M ops/s** |  **37x** | 1 |

*(Measured on a 13th Gen Intel i5-13500H, `go test -benchmem`. Details → [docs/benchmarks.md](docs/benchmarks.md))*

---

## Highlights

- **LSM-Tree engine** — MemTable (SkipList) → SSTable (mmap) + background flush/compaction
- **Hybrid Go + Rust** — hot-path storage primitives (SortedMap, Bloom) in Rust, bound via CGo FFI
- **Tiered Storage** — Hot cache (RAM/LRU) → Warm (OS-managed mmap) → Cold (disk)
- **RESP protocol server** — **backward compatible** with Redis / IgnisDB v1 clients
- **Write-Ahead Log** — crash recovery with CRC32 checksums
- **Bloom filters** — skip disk access on negative lookups
- **Sorted Sets (ZSET)** and **Full-Text Search** (TF-scored inverted index)
- **Single binary** — static build, no runtime dependencies

## Install & Build

Requirements: **Go ≥ 1.21**, **Rust (stable) + cargo**, **CGO_ENABLED=1**, a C toolchain (gcc).

```bash
# Build the Rust core and the server
make build

# Start the server
./bin/ignisdb-go --data-dir ./data --port 6380

# Tests (with race detector)
make test

# Benchmarks
make bench
```

> ⚠️ Without the Rust `libignisdb_core.a`, the CGo link fails. `make build` compiles it
> automatically; if you use raw `go build`, run `cd rust-core && cargo build --release` first.

## Quick Start

### Connect with redis-cli

```bash
redis-cli -p 6380
127.0.0.1:6380> SET name "IgnisDB-Go"
OK
127.0.0.1:6380> GET name
"IgnisDB-Go"
127.0.0.1:6380> ZADD leaderboard 100 alice 200 bob 150 charlie
(integer) 3
127.0.0.1:6380> ZRANGE leaderboard 0 -1
1) "alice"
2) "charlie"
3) "bob"
127.0.0.1:6380> INFO
```

### Use it embedded in Go

```go
package main

import (
    "fmt"

    "github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func main() {
    eng, err := engine.Open(engine.Config{DataDir: "./data"})
    if err != nil {
        panic(err)
    }
    defer eng.Close()

    eng.Put([]byte("hello"), []byte("world"))
    if val, ok := eng.Get([]byte("hello")); ok {
        fmt.Printf("hello => %s\n", val) // hello => world
    }
}
```

## Documentation

| Doc | Contents |
|-----|----------|
| [docs/getting-started.md](docs/getting-started.md) | Install, prerequisites, first run, troubleshooting |
| [docs/embedded-usage.md](docs/embedded-usage.md) | Go embedded API: engine, sorted set, search, client |
| [docs/server-protocol.md](docs/server-protocol.md) | RESP server and **all supported commands** |
| [docs/architecture.md](docs/architecture.md) | Hybrid Go+Rust architecture, LSM, tiered storage |
| [docs/configuration.md](docs/configuration.md) | Config fields, CLI flags, environment variables |
| [docs/api-reference.md](docs/api-reference.md) | Full Go API reference (type signatures) |
| [docs/benchmarks.md](docs/benchmarks.md) | Performance methodology and measurements |
| [docs/operations.md](docs/operations.md) | Production: flush, compaction, recovery, statistics |

> Every doc is also available in Turkish with the `_TR.md` suffix
> (e.g. [docs/getting-started_TR.md](docs/getting-started_TR.md)).

## Supported Commands (summary)

| Category | Commands |
|----------|----------|
| Strings  | `GET` `SET` `DEL` `EXISTS` `EXPIRE` `TTL` `PERSIST` |
| Lists    | `LPUSH` `RPUSH` `LRANGE` `LLEN` |
| Hashes   | `HSET` `HGET` `HGETALL` `HDEL` |
| Sets     | `SADD` `SMEMBERS` `SISMEMBER` `SREM` |
| Sorted Sets | `ZADD` `ZREM` `ZSCORE` `ZRANGE` `ZREVRANGE` `ZRANGEBYSCORE` `ZREVRANGEBYSCORE` `ZCARD` |
| Full-Text | `FT.ADD` `FT.SEARCH` |
| Utility  | `PING` `INFO` `AUTH` `CACHE.STATS` `MULTI` `EXEC` `DISCARD` `SUBSCRIBE` `PUBLISH` |

Full syntax and examples → [docs/server-protocol.md](docs/server-protocol.md)

## Project Structure

```
ignisdb-go/
├── cmd/server/            # Server entry point (main.go)
├── internal/
│   ├── engine/            # LSM engine (hybrid orchestration)
│   ├── memtable/          # SkipList MemTable
│   ├── sstable/           # SSTable + mmap + sparse index
│   ├── wal/               # Write-Ahead Log (buffered, CRC32)
│   ├── cache/             # LRU hot cache (write-back)
│   ├── bloom/             # Bloom filter
│   ├── sortedset/         # ZSET implementation
│   ├── search/            # Full-text inverted index
│   ├── rustcore/          # CGo FFI bridge (Rust static lib)
│   └── server/            # RESP protocol server
├── rust-core/             # Rust crate (BTreeMap SortedMap, fast Bloom)
├── pkg/client/            # Go client library
├── benchmarks/            # go test -bench performance suite
├── examples/              # Usage examples
└── tests/                 # Integration tests (7/7 pass)
```

## Architecture (brief)

```
┌───────────────────────────────────────────────┐
│  Go:  RESP Server · API · Orchestration       │  ← request routing, GC
├───────────────────────────────────────────────┤
│  CGo FFI Bridge (minimal boundary)            │  ← only hot primitives
├───────────────────────────────────────────────┤
│  Rust:  SortedMap (BTreeMap) · Fast Bloom     │  ← write path + negative lookup
├───────────────────────────────────────────────┤
│  Go:  MemTable · WAL · SSTable · LRU Cache    │  ← zero/near-zero alloc hot path
└───────────────────────────────────────────────┘
```

Why this choice? A full-Rust core was tried, but the CGo boundary crossing on every
operation (~1µs/op) noticeably slowed hot reads/writes. The hybrid approach keeps the
hot path in Go (near-zero allocation) and borrows Rust only where it wins. Details →
[docs/architecture.md](docs/architecture.md)

## License

[MIT](LICENSE)
