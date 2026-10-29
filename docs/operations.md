# Operations

🌐 **English** · [Türkçe](operations_TR.md)

Running IgnisDB-Go in production: lifecycle, flushing, compaction, recovery, monitoring,
and backup.

## Process Lifecycle

```bash
# foreground
./bin/ignisdb-go --data-dir /var/lib/ignisdb --host 0.0.0.0 --port 6380

# systemd-friendly: stop with SIGTERM/SIGINT
kill -TERM <pid>
```

On `SIGINT`/`SIGTERM` the server calls `Stop()` then `engine.Close()`:

1. active MemTable is **flushed** to an SSTable,
2. all SSTables are closed (mmap unmapped),
3. Rust SortedMap memory is freed,
4. the WAL is flushed and closed.

Always stop via signal — never `kill -9` a mid-write process unless the WAL is trusted for
replay.

## systemd Unit (sketch)

```ini
[Service]
ExecStart=/usr/local/bin/ignisdb-go --data-dir /var/lib/ignisdb --port 6380
Restart=on-failure
LimitNOFILE=65536
```

---

## Flushing

The engine flushes **automatically** when MemTable size crosses `MemtableSize` (default 16 MB).
You can force a flush for a clean checkpoint:

```go
eng.Flush()   // snapshot the active MemTable to an SSTable now
```

Flush is non-blocking on the hot path: it hands the immutable MemTable to the `flushLoop`
goroutine (or spawns one-off flush if the queue is full).

---

## Compaction

Compaction is **background-only** and automatic. When the number of SSTables crosses
`CompactionThreshold` (default 4), `compactLoop` merges all SSTables into one sorted run and
drops tombstones. There is **no public `Compact()`** method; observe it via stats.

Tuning:

| Situation | Change |
|-----------|--------|
| Read amplification too high (too many SSTables) | lower `CompactionThreshold` |
| Write amplification / I/O too high | raise `CompactionThreshold`, raise `MemtableSize` |
| Large key churn (many deletes) | lower `MemtableSize` so tombstones flush sooner |

```go
stats := eng.GetStats()
fmt.Printf("compactions so far: %v\n", stats["compactions"])
fmt.Printf("sstable_count: %v, total_bytes: %v\n", stats["sstable_count"], stats["sstable_total_bytes"])
```

---

## Crash Recovery

The WAL guarantees durability up to the last buffered flush. On `Open()`:

1. `loadSSTables()` reads every `sstable_*.db` (sorted), re-initializes the sequence counter,
2. `wal.Replay()` re-applies `WalPut`/`WalDelete` entries into the MemTable,
3. replay stops at the first CRC mismatch (partial/truncated write is discarded safely).

Because the WAL write buffer is **256 KB and flushed periodically**, a crash can lose the
last buffered entries not yet on disk. If you need per-write durability, call
`wal.Sync()` (not exposed on the engine API today).

```
Open(dir)
  → loadSSTables  (read sstable_*.db, restore sequence)
  → wal.OpenFile   (append mode)
  → replayWAL       (rebuild MemTable from log, CRC-check)
```

Verify recovery by restarting the server and reading a known key (see
[getting-started.md](getting-started.md#5-verifying-persistence)).

---

## Monitoring

`engine.GetStats()` returns a rich map:

| Key | What to watch |
|-----|---------------|
| `writes` / `reads` | throughput trend |
| `cache_hits` / `cache_misses` / `cache.hit_rate_pct` | raise `CacheSize` if hit rate is low |
| `sstable_reads` | read amplification into disk |
| `compactions` | should grow over time; stalls may signal I/O saturation |
| `memtable_entries` / `memtable_bytes` | approaching `MemtableSize` → flush pressure |
| `wal_size_bytes` | if it keeps growing, flush isn't keeping up |
| `sstable_total_bytes` | disk footprint |
| `rust_core_active` | should be `true`; `false` means silent Go fallback (slower) |

The server exposes the same via RESP:

```
127.0.0.1:6380> INFO
127.0.0.1:6380> CACHE.STATS
```

**Debug logging:** set `IGNISDB_DEBUG=1` to trace flush/compaction/WAL/replay events. Turn it
off in normal operation to avoid log noise.

---

## Backup & Restore

The data directory is self-contained: SSTables (`sstable_*.db`) + WAL (`wal.log`).

**Safe snapshot:**
1. force a flush (`eng.Flush()` or `redis-cli` `FLUSH`-equivalent isn't exposed — for the
   server path, prefer a clean restart, or snapshot at low traffic),
2. stop the process (`SIGTERM`) so the WAL is closed, or
3. copy the whole `--data-dir`.

**Restore:** copy the directory back and `Open()` it — SSTables + WAL replay rebuild state.

> No incremental/online backup exists yet. For hot backups, stop-and-copy or add a flush +
> `wal.Sync()` hook.

---

## Common Operations

| Task | How |
|------|-----|
| Change port/host | `--port` / `--host` flags |
| Reduce RAM footprint | lower `MemtableSize` / `CacheSize` (embedded) |
| Force durable writes | call `wal.Sync()` periodically (embedded) |
| Confirm Rust path active | `GetStats()["rust_core_active"]` |
| Inspect on disk | `ls -l $DATA_DIR` → `sstable_*.db`, `wal.log` |

---

## Troubleshooting

| Symptom | Likely cause | Action |
|---------|--------------|--------|
| High `wal_size_bytes`, never shrinks | flush/compaction can't keep up | raise disk I/O priority, lower `MemtableSize` |
| Low cache hit rate | `CacheSize` too small for hot set | raise `CacheSize` |
| `rust_core_active:false` | Rust lib not linked / init failed | rebuild with `make build`; check `CGO_ENABLED=1` |
| Data missing after crash | last buffered WAL not on disk | add periodic `Sync`; reduce write batching |
| Compaction stalls | single huge SSTable merge is I/O bound | split work by raising `CompactionThreshold` in bursts |
| Latency spikes | Go GC during flush | tune `GOGC`; reuse buffers |

## Related

- Architecture internals → [architecture.md](architecture.md)
- Tuning values → [configuration.md](configuration.md)
