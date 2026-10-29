# Mimari: Hibrit Go + Rust

🌐 [English](architecture.md) · **Türkçe**

Bu belge IgnisDB-Go'nun neden bu şekilde tasarlandığını, verinin sistem içinde
nasıl aktığını ve sıcak yol (hot path) optimizasyonlarını açıklar.

---

## 1. Tasarım Felsefesi: "Partial Hybrid"

Tamamen Go ya da tamamen Rust yerine **hibrit** seçildi. Gerekçe ölçümle ortaya çıktı:

| Denenen yaklaşım | Sonuç | Neden bırakıldı / seçildi |
|------------------|-------|----------------------------|
| **Saf Python v2** | ~50K ops/s write | Referans port; yavaş |
| **Saf Go (port)** | ~8.5M ops/s, 0 alloc | ✅ Seçildi (hot path Go) |
| **Go + Full Rust Core** | write ~1M ops/s | ❌ Her işlemde CGo sınırı (~1µs/op) sıcak yolu yavaşlattı |
| **Go + Rust Hybrid (Partial)** | **write ~7.6M, read ~5.6M, ~1 alloc/op** | ✅ **Seçildi** |

### Neden Full Rust Core yavaşladı?

Full-Rust'ta **her** Put/Get bir CGo fonksiyon çağrısıydı. CGo boundary geçişi tek başına
~30–50 ns değil; Rust→Go veri kopyalama (`Vec<u8>` → `C.GoBytes`) + goroutine/thread
karşılaşmasıyla birlikte çağrı başına ~1µs maliyet ekliyordu. Read sıcak yolu
65 ns'den (saf Go) 454 ns'ye (full Rust) çıktı.

### Hibrit'in kuralı

> **Yalnızca tek başına Go'dan belirgin hızlı olan primitifleri Rust'a taşı; arayı
> sıcak yolun dışına, tek bir FFI kapısına çek.**

Rust'a geçenler:
- **SortedMap** (MemTable için `BTreeMap`, Go GC/pointer-zinciri maliyeti yok)
- **Bloom filter hash'i** (xxhash-benzeri hızlı hash)

Go'da kalanlar (sıfır/near-sıfır allocation):
- MemTable SkipList (flush + iterasyon için), WAL, SSTable (mmap), LRU Cache,
  sorted set, full-text search, RESP sunucusu, orkestrasyon.

---

## 2. Katmanlar

```
┌───────────────────────────────────────────────────────────┐
│  pkg/client          RESP istemci (Go)                     │
├───────────────────────────────────────────────────────────┤
│  internal/server     RESP protocol parser + komut router   │  Go
│                      (in-memory lists/hashes/sets/ttl)     │
├───────────────────────────────────────────────────────────┤
│  internal/engine     LSM orkestrasyon (Lock, flush/compact)│  Go
├───────────────────────────────────────────────────────────┤
│  internal/rustcore   CGo FFI bridge                        │  Go↔Rust tek kapı
├───────────────────────────────────────────────────────────┤
│  rust-core/src/lib   SortedMap (BTreeMap) + Fast Bloom      │  Rust (staticlib)
├───────────────────────────────────────────────────────────┤
│  internal/memtable · wal · sstable · cache · bloom ·        │  Go
│  sortedset · search                                          │
└───────────────────────────────────────────────────────────┘
```

`rust-core`, `staticlib` olarak derlenir (`libignisdb_core.a`), LTO + opt-level=3. Go
tarafı `#cgo LDFLAGS` ile mutlak yolu linkler. Rust init başarısız olursa motor
`useRust=false` ile saf Go'ya zarif biçimde düşer (`recover()` + fallback).

---

## 3. Tiered Storage (3 katman)

| Katman | Ortam | Bileşen | Erişim |
|--------|-------|---------|--------|
| **Hot** | RAM | LRU Cache (vars. 64 MB) | pointer deref, O(1) |
| **Hot/Warm** | RAM | MemTable (16 MB) + immutables | SkipList lookup + Rust SortedMap |
| **Warm** | RAM (OS) | SSTable **mmap** sayfaları | page cache, zero-copy |
| **Cold** | Disk | SSTable dosyaları + WAL | bloker disk I/O |

### Okuma yolu (`Get`)

```
Get(key)
  1. LRU Cache hit?            →  DÖN (en sıcak)
  2. Rust SortedMap hit?       →  DÖN + cache'e koy
  3. Go SkipList (aktif) hit?  →  DÖN + cache'e koy
  4. Immutable memtables (yeni→eski)?  →  DÖN
  5. SSTables (yeni→eski):
        MightContain(key)?  ← Bloom (negatifse DISKI ATLA)
        SSTable.Get(key)    ← sparse index + mmap block scan
  6. yok → (nil, false)
```

**Bloom filtresi** negatif okumalarda disk/mmap erişimini tamamen atlar — read-cold
performansının anahtarı.

### Yazma yolu (`Put`)

```
Put(key,value)
  1. WAL.Append          →  buffered (256 KB bufio) + sync.Pool tampon + CRC32
  2. MemTable.Put        →  Go SkipList (node pool ile ~zero alloc)
  3. Cache GÜNCELLENMEZ  →  write-back politika (bkz. §4)
  4. memtableSize ≥ limit →  swap + flush'a al (arka plan)
```

Flush (arka plan `flushLoop`): MemTable → sıralı → `sstable.Create` (data blocks +
sparse index + bloom + footer) → SSTable listesine ekle → immutables'tan çıkar →
`WAL.Truncate`.

Compaction (arka plan `compactLoop`): L0 sayısı eşiği geçince `sstable.Merge` tüm
SSTable'ları tek sıralı dosyada birleştirir, tombstone'ları düşürür.

---

## 4. Sıcak Yol Optimizasyonları (near-zero alloc)

Motor seviyesinde ölçülen **~1 alloc/op**'un nasıl yakalandığı:

| Optimizasyon | Dosya | Kazanç |
|--------------|-------|--------|
| **SkipList node `sync.Pool`** | `memtable/skiplist.go` | put/iter'da node allocation → reuse (1024 node pre-warm) |
| **WAL buffer `sync.Pool`** | `wal/wal.go` | entry tamponu her yazmada yeniden alloc edilmez (256 buf pre-warm) |
| **xxhash-benzeri hızlı hash** | `bloom/bloom.go` | SHA-256 yerine ~10x hızlı, allocation-free hashing |
| **Write-back cache** | `cache/lru.go` | Yazmada cache güncellenmez → 2+ alloc/op silinir |
| **`appendInt` (bench)** | `benchmarks/` | `strconv.Itoa` yerine alloc'süz tamsayı→byte |
| **mmap zero-copy** | `sstable/sstable.go` | dosya içeriği kullanıcı buffer'ına kopyalanmaz |
| **Rust SortedMap** | `rust-core/lib.rs` | GC baskısı olmayan BTreeMap yazma sırası |

### Write-back cache tercihi

`Put` cache'i bilerek güncellemez. Cache yalnızca **okuma**da dolar:

```go
// Get miss → memtable/SSTable hit → cache.Put  (tek nokta)
// Put      → sadece WAL + MemTable              (cache'e dokunmaz)
```

Bu, `map` key/value kopyalama + LRU node alloc'ünü sıcak yazma yolundan çıkarır.
Tutarlılık korunur çünkü `Get` her zaman en taze kaynağı (memtable → sstable) okur ve
`Delete` cache'ten düşürür.

---

## 5. Bileşen Sorumlulukları

| Paket | Sorumluluk | Ana tip |
|-------|-----------|---------|
| `engine` | orkestrasyon, lock, flush/compact tetikleme, istatistik | `LSMEngine`, `Config` |
| `memtable` | sıralı in-memory yazma buffer'ı (flush + iter) | `SkipList` |
| `rustcore` | CGo köprüsü, Rust'a delege sıcak primitifler | `SortedMap`, `NewBloomFilter` |
| `sstable` | kalıcı sıralı dosya + mmap + sparse index + bloom | `SSTable` |
| `wal` | crash-güvenli append-only log (buffered, CRC32) | `WAL`, `Entry` |
| `cache` | fixed-size LRU hot cache | `LRUCache` |
| `bloom` | olası üyelik (negatif hızlı ret) | `BloomFilter` |
| `sortedset` | skorlu küme (ZSET) | `SortedSet` |
| `search` | inverted index + TF skor | `InvertedIndex`, `SearchResult` |
| `server` | RESP parse + komut routing + in-memory tipler | `Server` |

---

## 6. Eşzamanlılık

- `LSMEngine.mu` (`sync.RWMutex`): memtable/SSTable/sortedset yapısını korur.
  Okumalar `RLock` ile paralel; yazma/swaph/flush `Lock`.
- `flushMu`: flush'lar sıralanır.
- `compacting` (`atomic int32` + CAS): eşzamanlı compaction'ı engeller.
- Sayılar (`stats.*`) `atomic.Add/Load` ile korumalı.
- `flushCh`/`compactCh` kanalları: sıcak yol flush'ı **bloklamaz** — kanal doluysa
  `go doFlushMemtable`'a kaçar (drop-tail yerine async).

---

## 7. Crash Recovery Akışı

```
Open(dir):
  loadSSTables()   ← sstable_*.db (sıralı) + sequence yeniden kurulumu
  wal.OpenFile()   ← append mode
  replayWAL()      ← tüm WalPut/WalDelete'i MemTable'a geri uygula (CRC doğrula)
```

WAL'da CRC32 tutarsızsa replay o noktada durur (kısmi yazma = bozuk kayıt). Flush
sonrası `WAL.Truncate` (rename-to-.old + yeni boş dosya) log'u küçültür.

---

## Sonraki Adımlar

- Konfigürasyon değerleri → [configuration_TR.md](configuration_TR.md)
- Üretim operasyonları (flush/compact/stats) → [operations_TR.md](operations_TR.md)
