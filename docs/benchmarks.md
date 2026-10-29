# Benchmarks

🌐 **English** · [Türkçe](benchmarks_TR.md)

How performance is measured, the numbers on reference hardware, and how to reproduce them.

## Methodology

Benchmarks are the Go `testing` suite in `benchmarks/`. They measure the **embedded engine**
(no network, no RESP) so the numbers isolate the storage hot path.

Each `ns/op` is a **single-goroutine sequential latency**; ops/s is derived as `1e9 / ns_per_op`.
Go runs these on one goroutine by default, so they reflect per-operation cost, not aggregate
parallel throughput.

| Benchmark | What it does | Key setup |
|-----------|--------------|-----------|
| `BenchmarkWrite` | 10,000-entry warmup, then continuous `Put` | `key:N` / `value:N`, pooled buffers |
| `BenchmarkReadHot` | populate 10,000 keys, then `Get` cycling over them | data stays in memtable → cache hits |
| `BenchmarkReadCold` | populate 100,000 keys, `Flush` to SSTable, then `Get` | forces the SSTable/bloom path |
| `BenchmarkMixedWorkload` | 10% write / 90% read over a 10,000-key set | realistic mixed access |

**Allocation hygiene:** keys/values are built with a custom `appendInt` (no `strconv.Itoa`),
and a 10,000-entry warmup primes the `sync.Pool` caches so the timer measures steady state.

## How to Run

```bash
make bench          # builds Rust core first, then:
# CGO_ENABLED=1 go test -bench=. -benchmem ./benchmarks/

# Longer, more stable runs:
CGO_ENABLED=1 go test -bench=. -benchmem -benchtime=3s -count=5 ./benchmarks/
```

## Reference Results

Measured on **13th Gen Intel i5-13500H**, `CGO_ENABLED=1`, hybrid Rust core active,
`-benchtime=3s`. All four benchmarks report **~1 alloc/op**.

| Benchmark | ns/op | B/op | allocs/op | ≈ ops/s |
|-----------|-------|------|-----------|---------|
| Write | 131.8 | 4 | 1 | ~7.59M |
| Read (hot) | 178.8 | 16 | 1 | ~5.59M |
| Read (cold) | 201.3 | 15 | 1 | ~4.97M |
| Mixed | 246.4 | 16 | 1 | ~4.06M |

```
BenchmarkWrite-16           	 24544593	       131.8 ns/op	      4 B/op	       1 allocs/op
BenchmarkReadHot-16         	 19708806	       178.8 ns/op	     16 B/op	       1 allocs/op
BenchmarkReadCold-16        	 18088365	       201.3 ns/op	     15 B/op	       1 allocs/op
BenchmarkMixedWorkload-16   	 14217912	       246.4 ns/op	     16 B/op	       1 allocs/op
```

## Comparison vs Python v2

The Python reference (IgnisDB v2) ran its own suite on similar hardware:

| Operation | Python v2 | IgnisDB-Go (Hybrid) | Speedup |
|-----------|-----------|---------------------|---------|
| Write | 48,543 ops/s | ~7,587,000 ops/s | **~156x** |
| Read (hot) | 125,786 ops/s | ~5,592,800 ops/s | **~44x** |
| Read (cold) | 28,409 ops/s | ~4,967,700 ops/s | **~175x** |
| Mixed | 108,696 ops/s | ~4,058,400 ops/s | **~37x** |

The largest wins are on **write** and **read-cold**, where Go's mmap + bloom + pooled buffers
crush the pure-Python path.

## Architecture Evolution (why hybrid wins)

| Variant | Write ns/op | ReadHot ns/op | Hot allocs |
|---------|-------------|---------------|------------|
| Python v2 | ~20,600 | ~7,950 | — |
| Go (pure / hybrid hot path) | **~117–132** | **~65–179** | **0–1** |
| Go + **Full Rust Core** | ~1,042 | ~454 | 3–4 |
| Go + **Rust Hybrid (current)** | ~132 | ~179 | ~1 |

The full-Rust experiment regressed because every op crossed the CGo boundary (~1µs/op).
Reverting to the hybrid design (Rust only for SortedMap/Bloom, everything else in Go) restored
near-zero allocation and 5–8x better latency. See [architecture.md](architecture.md).

## Caveats

- These are **engine-level** numbers. End-to-end server throughput adds RESP parsing + TCP and
  is lower, and depends on connection count and value sizes.
- `read cold` uses a 100k-key set flushed to disk; results vary with SSTable count and OS page
  cache warmth.
- Allocs of ~1/op come from occasional memtable node/WAL buffer growth after pool warmup;
  steady-state is effectively allocation-free for the key path.

## Related

- Where the speed comes from → [architecture.md](architecture.md)
- Tuning the buffers/cache → [configuration.md](configuration.md)
