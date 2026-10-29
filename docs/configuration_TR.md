# Konfigürasyon

🌐 [English](configuration.md) · **Türkçe**

IgnisDB-Go üç yolla yapılandırılır: **CLI flag'leri** (sunucu), **`engine.Config`**
(gömülü), **ortam değişkenleri** (çalışma-zamanı davranışı).

---

## CLI Flag'leri (`cmd/server`)

```bash
./bin/ignisdb-go [--data-dir DIR] [--host IP] [--port N]
```

| Flag | Varsayılan | Tip | Açıklama |
|------|-----------|-----|----------|
| `--data-dir` | `./data` | string | SSTable + WAL dizini (yoksa oluşturulur) |
| `--host` | `127.0.0.1` | string | TCP bind adresi |
| `--port` | `6380` | int | RESP portu |

> Sunucu `engine.Config`'i **yalnızca `DataDir`** ile açar; MemTable/Cache/Compaction
> boyutları varsayılanı kullanır. Bunları ayarlamak için gömülü mod veya ileride eklenecek
> flag'ler gerekir.

---

## `engine.Config` (gömülü mod)

```go
engine.Open(engine.Config{
    DataDir:             "./data",
    MemtableSize:        16 * 1024 * 1024,
    CacheSize:           64 * 1024 * 1024,
    CompactionThreshold: 4,
})
```

| Alan | Tip | Varsayılan | Anlam | Ayar rehberi |
|------|-----|-----------|-------|--------------|
| `DataDir` | `string` | `./data` | veri dizini | kalıcı NVMe/SSD dizinini ver |
| `MemtableSize` | `int64` (byte) | `16 MB` | flush eşiği | ↑ = daha az flush/compaction, daha çok RAM |
| `CacheSize` | `int` (byte) | `64 MB` | hot LRU kapasitesi | ↑ = daha çok cache hit, daha çok RAM |
| `CompactionThreshold` | `int` | `4` | L0 SSTable sayısı | ↑ = yazma amplifikasyonu ↓ ama read amplifikasyonu ↑ |

Sıfır/negatif değer girilirse ilgili alan varsayılana düşer.

### Boyut seçimi (pratik)

| Senaryo | MemtableSize | CacheSize | CompactionThreshold |
|---------|--------------|-----------|---------------------|
| Low-latency KV | 16–32 MB | 64–128 MB | 4 |
| Write-heavy bulk | 64–128 MB | 32–64 MB | 4–6 |
| Read-heavy (hot set) | 8–16 MB | 128–256 MB | 2–4 |

> `CacheSize` hedef **byte** kapasitesidir; tek kayıt `cacheSize/10`'dan büyükse
> cache'e yazılmaz (dev tekil kayıt cache'i boğmasın).

---

## Ortam Değişkenleri

| Değişken | Etki |
|----------|------|
| `IGNISDB_DEBUG` | Ayarlıysa (boş olmayan) motor günlüklerini basar (flush/compaction/WAL/replay). Üretimde **kapalı** tutmak benchmark/günlük kirliliğini önler. |
| `CGO_ENABLED=1` | Rust FFI linklemesi için zorunlu (derleme-zamanı). |
| `GOPATH`, `PATH` | Build ortamı (go + cargo + gcc erişilebilir olmalı). |

Debug açma:

```bash
IGNISDB_DEBUG=1 ./bin/ignisdb-go --port 6380
# [time] Flushed MemTable to SSTable #3 (1024 entries)
# [time] Starting compaction (4 SSTables)
```

---

## Derleme Ayarları (`rust-core/Cargo.toml`)

```toml
[profile.release]
opt-level = 3
lto = true
codegen-units = 1
```

| Ayar | Neden |
|------|-------|
| `opt-level = 3` | Azami optimizasyon |
| `lto = true` | cross-crate inlining (hot hash + BTreeMap) |
| `codegen-units = 1` | daha sıkı optimizasyon (derleme yavaşlar, runtime hızlanır) |
| `crate-type = ["staticlib"]` | Go `#cgo LDFLAGS` `.a` bekler |

---

## Kaynak Limitleri (varsayılan çalışma)

| Kaynak | Tipik değer | Not |
|--------|-------------|-----|
| RAM | ~`MemtableSize + CacheSize` + OS page cache | mmap sayfaları OS yönetir |
| Disk | yazılan veri + WAL + compaction geçici | compaction sonrası küçülür |
| Goroutine | 2 arka plan (flushLoop, compactLoop) + 1/bağlantı | sunucu modu |
| FFI thread | Rust çağrıları CGo'da OS thread | goroutine→thread maliyeti (bu yüzden sıcak yol Go'da) |

---

## İlgili

- Motor tipleri → [api-reference_TR.md](api-reference_TR.md)
- Çalıştırma/operasyon → [operations_TR.md](operations_TR.md)
