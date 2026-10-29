# IgnisDB-Go

🌐 [English](README.md) · **Türkçe**

**High-performance LSM-Tree + Tiered Storage database**, Go'da yazılmıştır.
Python [IgnisDB](https://github.com/buraksocial/IgnisDB) v2'nin sıcak yolları (hot path)
Rust ile hızlandırılmış **hibrit (Go + Rust)** implementasyonudur.

> Write ve Read'de Python'a göre **~40-150x** daha hızlı, tüm sıcak işlemlerde **near-zero allocation**.

| Benchmark | Python v2 | IgnisDB-Go (Hybrid) | Hızlanma | Allocs/op |
|-----------|-----------|---------------------|----------|-----------|
| Write      |    48,543 ops/s | **~7.59M ops/s** | **156x**  | 1 |
| Read (hot) |   125,786 ops/s | **~5.59M ops/s** |  **44x**  | 1 |
| Read (cold)|    28,409 ops/s | **~4.97M ops/s** | **175x**  | 1 |
| Mixed      |   108,696 ops/s | **~4.06M ops/s** |  **37x**  | 1 |

*(Ölçüm: 13th Gen Intel i5-13500H, `go test -benchmem`. Detaylar → [docs/benchmarks_TR.md](docs/benchmarks_TR.md))*

---

## Öne Çıkan Özellikler

- **LSM-Tree motoru** — MemTable (SkipList) → SSTable (mmap) + arka plan flush/compaction
- **Hibrit Go + Rust** — Sıcak yol depolama primitifleri (SortedMap, Bloom) Rust'ta, CGo FFI ile bağlanır
- **Tiered Storage** — Hot cache (RAM/LRU) → Warm (OS-managed mmap) → Cold (disk)
- **RESP protokol sunucusu** — Redis / IgnisDB v1 istemcileriyle **geriye uyumlu**
- **Yazma-güvenli log (WAL)** — CRC32 checksum ile crash recovery
- **Bloom filter** — Negatif okumalarda disk erişimini atlar
- **Sorted Sets (ZSET)** ve **Full-Text Search** (TF skorlu inverted index)
- **Tek binary** — Statik derleme, runtime bağımlılığı yok

## Kurulum & Build

Gereksinimler: **Go ≥ 1.21**, **Rust (stable) + cargo**, **CGO_ENABLED=1**, C toolchain (gcc).

```bash
# Rust çekirdeğini derle ve sunucuyu oluştur
make build

# Servisi başlat
./bin/ignisdb-go --data-dir ./data --port 6380

# Testler (race detector ile)
make test

# Benchmarklar
make bench
```

> ⚠️ Rust `libignisdb_core.a` olmadan CGo link başarısız olur. `make build` bunu otomatik derler;
> ham `go build` kullanıyorsan önce `cd rust-core && cargo build --release` çalıştır.

## Hızlı Başlangıç

### redis-cli ile bağlan

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

### Gömülü (embedded) Go olarak kullan

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

    eng.Put([]byte("merhaba"), []byte("dünya"))
    if val, ok := eng.Get([]byte("merhaba")); ok {
        fmt.Printf("merhaba => %s\n", val) // merhaba => dünya
    }
}
```

## Dokümantasyon

| Belge | İçerik |
|-------|--------|
| [docs/getting-started_TR.md](docs/getting-started_TR.md) | Kurulum, ön koşul, ilk çalıştırma, sorun giderme |
| [docs/embedded-usage_TR.md](docs/embedded-usage_TR.md) | Go gömülü API: engine, sorted set, search, istemci |
| [docs/server-protocol_TR.md](docs/server-protocol_TR.md) | RESP sunucusu ve **tüm desteklenen komutlar** |
| [docs/architecture_TR.md](docs/architecture_TR.md) | Hibrit Go+Rust mimarisi, LSM, tiered storage |
| [docs/configuration_TR.md](docs/configuration_TR.md) | Config alanları, CLI flag'leri, ortam değişkenleri |
| [docs/api-reference_TR.md](docs/api-reference_TR.md) | Tam Go API referansı (tip imzaları) |
| [docs/benchmarks_TR.md](docs/benchmarks_TR.md) | Performans metodolojisi ve ölçümler |
| [docs/operations_TR.md](docs/operations_TR.md) | Üretim: flush, compaction, recovery, istatistikler |

## Desteklenen Komutlar (özet)

| Kategori | Komutlar |
|----------|----------|
| Strings  | `GET` `SET` `DEL` `EXISTS` `EXPIRE` `TTL` `PERSIST` |
| Lists    | `LPUSH` `RPUSH` `LRANGE` `LLEN` |
| Hashes   | `HSET` `HGET` `HGETALL` `HDEL` |
| Sets     | `SADD` `SMEMBERS` `SISMEMBER` `SREM` |
| Sorted Sets | `ZADD` `ZREM` `ZSCORE` `ZRANGE` `ZREVRANGE` `ZRANGEBYSCORE` `ZREVRANGEBYSCORE` `ZCARD` |
| Full-Text | `FT.ADD` `FT.SEARCH` |
| Utility  | `PING` `INFO` `AUTH` `CACHE.STATS` `MULTI` `EXEC` `DISCARD` `SUBSCRIBE` `PUBLISH` |

Tam sözdizim ve örnekler → [docs/server-protocol_TR.md](docs/server-protocol_TR.md)

## Proje Yapısı

```
ignisdb-go/
├── cmd/server/            # Sunucu giriş noktası (main.go)
├── internal/
│   ├── engine/            # LSM motoru (hibrit orkestrasyon)
│   ├── memtable/          # SkipList MemTable
│   ├── sstable/           # SSTable + mmap + sparse index
│   ├── wal/               # Write-Ahead Log (buffered, CRC32)
│   ├── cache/             # LRU hot cache (write-back)
│   ├── bloom/             # Bloom filter
│   ├── sortedset/         # ZSET implementasyonu
│   ├── search/            # Full-text inverted index
│   ├── rustcore/          # CGo FFI bridge (Rust static lib)
│   └── server/            # RESP protokol sunucusu
├── rust-core/             # Rust depocusu (BTreeMap SortedMap, fast Bloom)
├── pkg/client/            # Go istemci kütüphanesi
├── benchmarks/            # go test -bench performans ölçümleri
├── examples/              # Kullanım örnekleri
└── tests/                 # Entegrasyon testleri (7/7 geçer)
```

## Mimari (kısa)

```
┌───────────────────────────────────────────────┐
│  Go:  RESP Server · API · Orkestrasyon        │  ← istek路由, GC
├───────────────────────────────────────────────┤
│  CGo FFI Bridge (minimal boundary)            │  ← yalnızca sıcak primitifler
├───────────────────────────────────────────────┤
│  Rust:  SortedMap (BTreeMap) · Fast Bloom     │  ← yazma yolu + negatif lookup
├───────────────────────────────────────────────┤
│  Go:  MemTable · WAL · SSTable · LRU Cache    │  ← zero/near-zero alloc sıcak yol
└───────────────────────────────────────────────┘
```

Neden bu tercih? Tam-Rust çekirdek denendi ama her işlemde CGo sınır geçişi (~1µs/op) sıcak
okuma/yazmayı gözle görülür yavaşlattı. Hibrit yaklaşım sıcak yolu Go'da (zero-alloc) tutar,
yalnızca uygun yerlerde Rust'tan yararlanır. Detaylar → [docs/architecture_TR.md](docs/architecture_TR.md)

## Lisans

[MIT](LICENSE)
