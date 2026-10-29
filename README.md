# IgnisDB-Go

High-performance LSM-Tree + Tiered Storage database engine written in Go.
A 10x faster reimplementation of [IgnisDB](https://github.com/buraksocial/IgnisDB) (Python).

## Features

- **LSM-Tree Storage Engine**: MemTable (SkipList) → SSTable (mmap) with background compaction
- **RESP Protocol Server**: Backward compatible with Redis/IgnisDB v1 clients
- **Hot Cache**: Fixed-size LRU cache (64 MB default) for frequently accessed keys
- **Bloom Filters**: Probabilistic set membership for fast negative lookups
- **Write-Ahead Log**: Crash recovery with CRC32 checksums
- **Sorted Sets**: Redis ZSET compatible secondary indexes
- **Full-Text Search**: Inverted index with TF-IDF scoring
- **Zero-Copy Reads**: Native mmap via syscall.Mmap
- **Single Binary**: ~15-20 MB static binary, no runtime dependencies

## Architecture

```
┌─────────────────────────────────────┐
│  Hot Cache (RAM) — 64 MB fixed     │
│  ├── LRU cache (10K keys)          │
│  ├── MemTable (SkipList, 16 MB)    │
│  └── Bloom filters (8 MB)          │
├─────────────────────────────────────┤
│  Warm Cache (mmap) — OS managed    │
│  └── SSTable files (memory-mapped) │
├─────────────────────────────────────┤
│  Cold Storage (Disk) — Unlimited   │
│  ├── SSTables (sorted, compressed) │
│  ├── WAL (crash recovery)          │
│  └── Compaction output             │
└─────────────────────────────────────┘
```

## Quick Start

### Build & Run

```bash
make build
./bin/ignisdb-go --data-dir ./data --port 6380
```

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
```

### Embedded Usage

```go
import "github.com/buraksocial/IgnisDB-Go/internal/engine"

eng, _ := engine.Open(engine.Config{DataDir: "./data"})
defer eng.Close()

eng.Put([]byte("key"), []byte("value"))
val, ok := eng.Get([]byte("key"))
```

## Supported Commands

| Category | Commands |
|----------|----------|
| Strings | GET, SET, DEL, EXISTS, EXPIRE, TTL, PERSIST |
| Lists | LPUSH, RPUSH, LRANGE, LLEN |
| Hashes | HSET, HGET, HGETALL, HDEL |
| Sets | SADD, SMEMBERS, SISMEMBER, SREM |
| Sorted Sets | ZADD, ZREM, ZSCORE, ZRANGE, ZREVRANGE, ZRANGEBYSCORE, ZREVRANGEBYSCORE, ZCARD |
| Full-Text Search | FT.ADD, FT.SEARCH |
| Utility | PING, INFO, AUTH, CACHE.STATS |

## Performance Targets

| Metric | Python v2 | Go Target |
|--------|-----------|-----------|
| Write | 50K ops/sec | 500K ops/sec |
| Read (hot) | 200K ops/sec | 2M ops/sec |
| Read (cold) | 50K ops/sec | 500K ops/sec |
| RAM overhead | 64-88 MB | 32-48 MB |
| Startup time | 500ms | 10ms |

## Testing

```bash
make test    # Run all tests with race detector
make bench   # Run benchmarks
```

## Project Structure

```
ignisdb-go/
├── cmd/server/main.go           # Entry point
├── internal/
│   ├── engine/engine.go         # LSM Engine
│   ├── memtable/skiplist.go     # SkipList MemTable
│   ├── sstable/sstable.go       # SSTable + mmap
│   ├── wal/wal.go               # Write-Ahead Log
│   ├── cache/lru.go             # LRU hot cache
│   ├── bloom/bloom.go           # Bloom filter
│   ├── sortedset/sortedset.go   # Sorted sets
│   ├── search/inverted.go       # Full-text search
│   └── server/resp.go           # RESP protocol server
├── pkg/client/client.go         # Go client library
├── benchmarks/                  # Performance benchmarks
├── examples/                    # Usage examples
└── tests/                       # Integration tests
```

## License

MIT
