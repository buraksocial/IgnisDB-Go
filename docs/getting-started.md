# Getting Started

🌐 **English** · [Türkçe](getting-started_TR.md)

This guide shows how to install, build, and write your first data with IgnisDB-Go from scratch.

## Prerequisites

| Tool | Version | Why it's needed |
|------|---------|-----------------|
| **Go** | ≥ 1.21 | Engine and server |
| **Rust (cargo)** | stable (1.75+) | `libignisdb_core.a` — hot-path primitives |
| **C toolchain (gcc)** | any | CGo links the Rust static lib into Go |
| **CGO_ENABLED=1** | — | Usually 1 by default; verify it |

Check your versions:

```bash
go version
cargo --version
gcc --version
```

> **Why Rust + CGo?** IgnisDB-Go delegates the write-path sorted map (SortedMap) and the
> Bloom filter hash to Rust. These compile into a static library under `rust-core/` and are
> called from Go via CGo FFI. Therefore a bare `go build` is not enough; the Rust core must
> be built first.

---

## 1. Building

### Method A — `make` (recommended)

`make` compiles the Rust core automatically, then produces the Go binary:

```bash
git clone https://github.com/buraksocial/IgnisDB-Go.git
cd IgnisDB-Go
make build          # → rust-core: cargo build --release  +  go build ./cmd/server
```

Output: `bin/ignisdb-go`

### Method B — manual

```bash
# 1) Rust static library
cd rust-core && cargo build --release && cd ..

# 2) Go server (the CGo linker must find libignisdb_core.a)
CGO_ENABLED=1 go build -ldflags="-s -w" -o bin/ignisdb-go ./cmd/server
```

### Method C — tests / benchmarks

```bash
make test           # go test -v -race ./...  (Rust core built first)
make bench          # go test -bench=. -benchmem ./benchmarks/
```

---

## 2. Running the Server

```bash
./bin/ignisdb-go --data-dir ./data --host 127.0.0.1 --port 6380
```

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `./data` | SSTable + WAL directory |
| `--host` | `127.0.0.1` | Bind address |
| `--port` | `6380` | RESP protocol port (chosen ≠ Redis 6379) |

Log lines are printed only when `IGNISDB_DEBUG` is set (so benchmarks/behavior stay clean).

```bash
IGNISDB_DEBUG=1 ./bin/ignisdb-go   # enable quiet-mode logging, trace flush/compaction events
```

Shutdown: `Ctrl-C` → the signal handler calls `srv.Stop()` + `eng.Close()`, flushes the
MemTable to disk, and closes the WAL.

---

## 3. First Data — with `redis-cli`

Because IgnisDB-Go speaks RESP, any Redis client works:

```bash
redis-cli -p 6380
127.0.0.1:6380> PING
PONG
127.0.0.1:6380> SET user:1 "Ada Lovelace"
OK
127.0.0.1:6380> GET user:1
"Ada Lovelace"
127.0.0.1:6380> SET session:abc tok123 EX 300   # 300s TTL
OK
127.0.0.1:6380> TTL session:abc
(integer) 299
127.0.0.1:6380> EXISTS user:1
(integer) 1
127.0.0.1:6380> ZADD leaderboard 100 alice 200 bob 150 charlie
(integer) 3
127.0.0.1:6380> ZRANGE leaderboard 0 -1
1) "alice"
2) "charlie"
3) "bob"
127.0.0.1:6380> INFO
```

Without `redis-cli`, you can also probe raw TCP:

```bash
printf '*2\r\n$4\r\nPING\r\n' | nc -q1 127.0.0.1 6380
# +PONG
```

Full command syntax → [server-protocol.md](server-protocol.md)

---

## 4. First Data — Embedded Go

You can embed the engine directly in your app, with no server needed:

```go
package main

import (
    "fmt"

    "github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func main() {
    eng, err := engine.Open(engine.Config{
        DataDir:      "./data",
        MemtableSize: 16 * 1024 * 1024, // 16 MB
        CacheSize:    64 * 1024 * 1024, // 64 MB
    })
    if err != nil {
        panic(err)
    }
    defer eng.Close()

    eng.Put([]byte("hello"), []byte("world"))
    if v, ok := eng.Get([]byte("hello")); ok {
        fmt.Printf("hello => %s\n", v) // hello => world
    }
}
```

> **Note:** the `internal/` package can only be imported from within the same module. To use
> it in an external app, either add a `replace` directive to your own module, or run the server
> and use the [pkg/client](embedded-usage.md#the-go-client-library) client.

---

## 5. Verifying Persistence

The engine is an LSM-Tree: writes go to WAL + MemTable first, and flush to SSTable once the
size threshold is hit. A safe shutdown (`Ctrl-C` / `eng.Close()`) flushes the remaining
MemTable. On restart, data returns via SSTable + WAL replay:

```bash
# write
./bin/ignisdb-go & sleep 1
redis-cli -p 6380 SET persist "yes"
kill -INT %1
# restart and read
./bin/ignisdb-go & sleep 1
redis-cli -p 6380 GET persist   # => "yes"
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `undefined reference to ignis_engine_put` | Rust core not built, or linker path wrong | `cd rust-core && cargo build --release`, then `make build` |
| `build constraints exclude all Go files ... cgo` | `CGO_ENABLED=0` | `export CGO_ENABLED=1` |
| `C compiler gcc not found` | No C toolchain | `apt install build-essential` |
| `engine not available at ... (rust core)` | Rust FFI init failed → falls back to Go | Normal; performance becomes pure-Go instead of hybrid. Debug with `IGNISDB_DEBUG=1` |
| Port 6380 in use | Another instance | Use `--port 6381` |

Next step: [embedded-usage.md](embedded-usage.md) → learn the Go API in detail.
