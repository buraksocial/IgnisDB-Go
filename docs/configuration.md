# Configuration

🌐 **English** · [Türkçe](configuration_TR.md)

IgnisDB-Go is configured in three ways: **CLI flags** (server), **`engine.Config`** (embedded),
and **environment variables** (runtime behavior).

---

## CLI Flags (`cmd/server`)

```bash
./bin/ignisdb-go [--data-dir DIR] [--host IP] [--port N]
```

| Flag | Default | Type | Description |
|------|---------|------|-------------|
| `--data-dir` | `./data` | string | SSTable + WAL directory (created if missing) |
| `--host` | `127.0.0.1` | string | TCP bind address |
| `--port` | `6380` | int | RESP port |

> The server opens `engine.Config` with **only `DataDir`**; MemTable/Cache/Compaction sizes use
> their defaults. To tune those, use embedded mode or add flags in the future.

---

## `engine.Config` (embedded mode)

```go
engine.Open(engine.Config{
    DataDir:             "./data",
    MemtableSize:        16 * 1024 * 1024,
    CacheSize:           64 * 1024 * 1024,
    CompactionThreshold: 4,
})
```

| Field | Type | Default | Meaning | Tuning guide |
|-------|------|---------|---------|--------------|
| `DataDir` | `string` | `./data` | data directory | point to a persistent NVMe/SSD path |
| `MemtableSize` | `int64` (bytes) | `16 MB` | flush threshold | ↑ = fewer flush/compaction, more RAM |
| `CacheSize` | `int` (bytes) | `64 MB` | hot LRU capacity | ↑ = more cache hits, more RAM |
| `CompactionThreshold` | `int` | `4` | L0 SSTable count | ↑ = lower write amplification but higher read amplification |

A zero/negative value falls back to the default for that field.

### Sizing (practical)

| Scenario | MemtableSize | CacheSize | CompactionThreshold |
|----------|--------------|-----------|---------------------|
| Low-latency KV | 16–32 MB | 64–128 MB | 4 |
| Write-heavy bulk | 64–128 MB | 32–64 MB | 4–6 |
| Read-heavy (hot set) | 8–16 MB | 128–256 MB | 2–4 |

> `CacheSize` is a target **byte** capacity; a single record larger than `cacheSize/10` is not
> cached (so one huge entry can't choke the cache).

---

## Environment Variables

| Variable | Effect |
|----------|--------|
| `IGNISDB_DEBUG` | If set (non-empty), prints engine logs (flush/compaction/WAL/replay). Keep **off** in production to avoid benchmark/log pollution. |
| `CGO_ENABLED=1` | Required for the Rust FFI linkage (build-time). |
| `GOPATH`, `PATH` | Build environment (go + cargo + gcc must be reachable). |

Enabling debug:

```bash
IGNISDB_DEBUG=1 ./bin/ignisdb-go --port 6380
# [time] Flushed MemTable to SSTable #3 (1024 entries)
# [time] Starting compaction (4 SSTables)
```

---

## Build Settings (`rust-core/Cargo.toml`)

```toml
[profile.release]
opt-level = 3
lto = true
codegen-units = 1
```

| Setting | Why |
|---------|-----|
| `opt-level = 3` | maximum optimization |
| `lto = true` | cross-crate inlining (hot hash + BTreeMap) |
| `codegen-units = 1` | tighter optimization (slower compile, faster runtime) |
| `crate-type = ["staticlib"]` | the Go `#cgo LDFLAGS` expects a `.a` |

---

## Resource Limits (typical default operation)

| Resource | Typical value | Note |
|----------|---------------|------|
| RAM | ~`MemtableSize + CacheSize` + OS page cache | mmap pages are OS-managed |
| Disk | written data + WAL + transient compaction output | shrinks after compaction |
| Goroutines | 2 background (flushLoop, compactLoop) + 1/connection | server mode |
| FFI threads | Rust calls run on an OS thread in CGo | goroutine→thread cost (this is why the hot path stays in Go) |

---

## Related

- Engine types → [api-reference.md](api-reference.md)
- Running/operations → [operations.md](operations.md)
