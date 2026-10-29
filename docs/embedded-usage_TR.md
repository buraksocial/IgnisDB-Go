# Gömülü Kullanım (Embedded Go API)

🌐 [English](embedded-usage.md) · **Türkçe**

Bu belge, IgnisDB-Go motorunu bir sunucuya ihtiyaç duymadan kendi Go
uygulaman içinde nasıl kullanacağını anlatır.

> **Modül sınırı:** Motor `internal/` paketindedir ve Go kuralları gereği yalnızca
> aynı modül içinden import edilebilir. Harici bir projede kullanmak için:
>
> ```
> require github.com/buraksocial/IgnisDB-Go v0.0.0
> replace github.com/buraksocial/IgnisDB-Go => /mutlak/yol/IgnisDB-Go
> ```
>
> Veya HTTP/TCP katmanı olarak sunucuyu başlat ve [pkg/client](#istemci-go-client-kütüphanesi)
> istemcisini kullan.

---

## 1. Motoru Açma (Open)

```go
import "github.com/buraksocial/IgnisDB-Go/internal/engine"

eng, err := engine.Open(engine.Config{
    DataDir:      "./data",   // varsayılan "./data"
    MemtableSize: 16 << 20,   // 16 MB   (varsayılan DefaultMemtableSize)
    CacheSize:    64 << 20,   // 64 MB   (varsayılan DefaultCacheSize)
    // CompactionThreshold: 4,        // L0 SSTable sayısı (varsayılan 4)
})
```

| Config alanı | Tip | Varsayılan | Anlam |
|--------------|-----|-----------|-------|
| `DataDir` | `string` | `./data` | SSTable + WAL dizini (otomatik oluşturulur) |
| `MemtableSize` | `int64` | `16 MB` | Bu bayta ulaşınca MemTable flush'a alınır |
| `CacheSize` | `int` | `64 MB` | Hot LRU cache kapasitesi (byte) |
| `CompactionThreshold` | `int` | `4` | Bu L0 SSTable sayısında arka plan compaction tetiklenir |

`Open`:
1. dizini oluşturur,
2. mevcut SSTable'ları yükler (`sstable_*.db`),
3. WAL'ı açar ve `Replay()` ile MemTable'ı yeniden kurar,
4. arka plan `flushLoop` + `compactLoop` goroutine'lerini başlatır,
5. Rust çekirdeği varsa (`useRust=true`) hibrit modda döner; yoksa saf Go'ya düşer.

**Her zaman `defer eng.Close()` kullan** — kalan MemTable'ı flush eder, SSTable'ları
kapatır, Rust kaynaklarını serbest bırakır, WAL'ı kapatır.

---

## 2. Temel Anahtar-Değer: Put / Get / Delete

```go
// Put: anahtar-değer yazar. WAL + MemTable'a gider.
if err := eng.Put([]byte("user:1"), []byte("Ada")); err != nil {
    log.Fatal(err)
}

// Get: (value, found) döner. Cache → MemTable(rust) → MemTable(go) → immutables → SSTable.
val, ok := eng.Get([]byte("user:1"))
if !ok {
    fmt.Println("bulunamadı")
} else {
    fmt.Printf("user:1 => %s\n", val)
}

// Delete: tombstone yazar, cache'ten düşer. Motor seviyesinde her zaman true döner.
eng.Delete([]byte("user:1"))
_, ok = eng.Get([]byte("user:1")) // ok == false
```

### Put/Get/Flush döngüsü (LSM)

```
Put(key, value)
   │
   ├── WAL.Append  (crash güvenliği, buffered, CRC32)
   ├── MemTable.Put (Go SkipList; sıcak yol, node-pool ile ~zero alloc)
   │        cache GÜNCELLENMEZ  ← write-back politika (2+ alloc/op tasarruf)
   └── MemTable.Size ≥ limit ?  → flush'a al + yeni MemTable + arka planda SSTable yaz
```

**Write-back cache:** Bir yazma cache'i güncellemez. Cache yalnızca **okuma sırasında**
(cache miss → memtable/SSTable hit → `cache.Put`) dolar. Bu, yazma sıcak yolundan 2+ alloc'ü
kaldırır. Tazelik için Delete cache'ten düşürür, Get ise her zaman en yeni kaynağı (memtable →
SSTable) okur.

---

## 3. Zorla Flush ve Disk Durumu

```go
// Flush: aktif MemTable'ı hemen bir SSTable'a yaz (örn. yedekleme öncesi).
eng.Flush()

n := eng.SSTableCount()          // aktif SSTable sayısı
sz := eng.MemtableSize()         // bayt cinsinden aktif memtable boyutu
fmt.Printf("%d SSTable, memtable=%d B\n", n, sz)
```

Compaction arka planda otomatik çalışır (L0 eşik aşılınca `sstable.Merge`). Manuel bir
`Compact()` public metodu yoktur; `GetStats()` içindeki `compactions` sayacı kaç tur
çalıştığını gösterir.

---

## 4. Sorted Sets (ZSET)

Redis ZSET'e benzer, skoru float64, üyeler byte slice:

```go
key := []byte("leaderboard")

added := eng.ZAdd(key, 100, []byte("alice"))   // int: 1 yeni eklendi, 0 güncellendi
eng.ZAdd(key, 200, []byte("bob"))
eng.ZAdd(key, 150, []byte("charlie"))

card := eng.ZCard(key)                         // 3

// Skora göre artan sıralı aralık (0..-1 = tümü)
members := eng.ZRange(key, 0, -1)              // [alice, charlie, bob]

// Azalan sıralı
rev := eng.ZRevRange(key, 0, -1)               // [bob, charlie, alice]

// Skor aralığı + limit (offset, limit)
scoreRange := eng.ZRangeByScore(key, 100, 160, 0, 10)  // [alice, charlie]
revRange   := eng.ZRevRangeByScore(key, 160, 100, 0, -1)

// Tek üye skoru
if s, ok := eng.ZScore(key, []byte("bob")); ok {
    fmt.Printf("bob=%.0f\n", s)                 // bob=200
}

// Üye silme (int: silinen sayısı)
eng.ZRem(key, []byte("charlie"))
```

| Metod | İmza | Dönüş |
|-------|------|-------|
| `ZAdd` | `(key []byte, score float64, member []byte)` | `int` (1 eklendi / 0 güncellendi) |
| `ZRem` | `(key, member []byte)` | `int` (silinen sayısı) |
| `ZScore` | `(key, member []byte)` | `(float64, bool)` |
| `ZRange` | `(key []byte, start, stop int)` | `[][]byte` (artan) |
| `ZRevRange` | `(key []byte, start, stop int)` | `[][]byte` (azalan) |
| `ZRangeByScore` | `(key []byte, min, max float64, offset, limit int)` | `[][]byte` |
| `ZRevRangeByScore` | `(key []byte, max, min float64, offset, limit int)` | `[][]byte` |
| `ZCard` | `(key []byte)` | `int` |

`start/stop` negatif olabilir (`-1` = son üye). `limit < 0` → sınır yok.

---

## 5. Full-Text Search (FT)

Basit inverted index + TF (term frequency) skoru:

```go
eng.FTAdd("doc1", "Go is a fast compiled language")
eng.FTAdd("doc2", "Python is great for data science")
eng.FTAdd("doc3", "Go databases are very performant")

results := eng.FTSearch("go", 10)   // limit: en iyi N sonuç
for _, r := range results {
    fmt.Printf("%s (score=%.4f)\n", r.DocID, r.Score)
}
// doc1 ...   doc3 ...  (her ikisi "go" terimini içerir)

eng.FTRemove("doc1")                 // belgeden tüm terimleri çıkar
```

`SearchResult` tipi:

```go
type SearchResult struct {
    DocID string
    Score float64
}
```

> Skor ham term-frequency'dir, uzunluk-normalizasyonu içermez. Sorgu boş terimlere
> ayrılır ve sonuçlar skora göre azalan sıralanır.

---

## 6. İstatistikler

```go
stats := eng.GetStats()
fmt.Printf("%+v\n", stats)
```

`map[string]interface{}` döner; anahtarlar:

| Anahtar | Tip | Anlam |
|---------|-----|-------|
| `writes` / `reads` | `int64` | toplam Put / Get sayısı |
| `cache_hits` / `cache_misses` | `int64` | LRU cache isabet / kaçırma |
| `sstable_reads` | `int64` | SSTable'dan okunan kayıt |
| `compactions` | `int64` | tamamlanan compaction turu |
| `memtable_entries` / `memtable_bytes` | `int` | aktif MemTable içeriği |
| `sstable_count` / `sstable_total_bytes` | `int` / `int64` | disk depolama |
| `wal_size_bytes` | `int64` | WAL dosya boyutu |
| `sorted_set_count` / `search_doc_count` | `int` | ikincil yapı sayıları |
| `rust_core_active` | `bool` | hibrit Rust yolu aktif mi |
| `cache` | `map[string]interface{}` | `hits`,`misses`,`hit_rate_pct`,`hot_cache_bytes` |

---

## İstemci (Go Client Kütüphanesi)

Sunucuya TCP/RESP üzerinden bağlanan hafif istemci — motoru gömmek istemediğinde:

```go
import "github.com/buraksocial/IgnisDB-Go/pkg/client"

c, err := client.Connect("127.0.0.1:6380")
if err != nil {
    log.Fatal(err)
}
defer c.Close()

c.Set("user:1", "Ada Lovelace")
v, _ := c.Get("user:1")        // v == "Ada Lovelace"
n, _ := c.Del("user:1")        // silinen anahtar sayısı (int64)
pong, _ := c.Ping()            // "PONG"
```

| Metod | Dönüş | Not |
|-------|-------|-----|
| `Connect(addr string)` | `(*Client, error)` | TCP dial + buffered reader/writer |
| `Close()` | `error` | bağlantıyı kapat |
| `Set(key, value string)` | `error` | RESP `SET` |
| `Get(key string)` | `(string, error)` | yoksa boş string |
| `Del(keys ...string)` | `(int64, error)` | silinen sayısı |
| `Ping()` | `(string, error)` | `PONG` |

> İstemci **yalnızca string tabanlı temel komutları** (SET/GET/DEL/PING) sarar.
> ZSET / FT / list / hash için ya motoru göm ya da ham RESP komutu gönderen kendi
> istemcini yaz (`sendCommand`/`readResponse` mantığı örnek alınabilir).

---

## 8. Tam Örnek

`examples/basic_usage.go` — motoru açıp Put/Get/Delete, sorted set ve search yapar:

```go
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func main() {
    dir := "./example_data"
    defer os.RemoveAll(dir)

    eng, err := engine.Open(engine.Config{DataDir: dir})
    if err != nil {
        log.Fatal(err)
    }
    defer eng.Close()

    // KV
    eng.Put([]byte("name"), []byte("IgnisDB-Go"))
    v, _ := eng.Get([]byte("name"))
    fmt.Printf("name = %s\n", v)

    // Sorted set
    k := []byte("board")
    eng.ZAdd(k, 100, []byte("alice"))
    eng.ZAdd(k, 200, []byte("bob"))
    eng.ZAdd(k, 150, []byte("charlie"))
    fmt.Println("ranked:", eng.ZRange(k, 0, -1)) // [alice charlie bob]

    // Search
    eng.FTAdd("d1", "Go is fast")
    eng.FTAdd("d2", "Go databases performant")
    for _, r := range eng.FTSearch("go", 10) {
        fmt.Printf("  %s %.2f\n", r.DocID, r.Score)
    }

    // Flush + stats
    eng.Flush()
    fmt.Printf("SSTables=%d\n", eng.SSTableCount())
}
```

Çalıştır:

```bash
CGO_ENABLED=1 go run ./examples/basic_usage.go
```

---

## Sonraki Adımlar

- Komut yüzeyinin tamamı → [server-protocol_TR.md](server-protocol_TR.md)
- Neden hibrit + veri yolu mimarisi → [architecture_TR.md](architecture_TR.md)
- Tip imzalarının tam listesi → [api-reference_TR.md](api-reference_TR.md)
