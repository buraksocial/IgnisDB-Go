# Getting Started

🌐 [English](getting-started.md) · **Türkçe**

Bu rehber IgnisDB-Go'yu sıfırdan kurmanı, derlemen ve ilk verini yazmanı gösterir.

## Ön Koşullar

| Araç | Sürüm | Neden gerekli |
|------|-------|----------------|
| **Go** | ≥ 1.21 | Motor ve sunucu |
| **Rust (cargo)** | stable (1.75+) | `libignisdb_core.a` — sıcak yol primitifleri |
| **C toolchain (gcc)** | herhangi | CGo, Rust statik lib'i Go'ya bağlar |
| **CGO_ENABLED=1** | — | Varsayılan genelde 1'dir; kontrol et |

Sürümleri doğrula:

```bash
go version
cargo --version
gcc --version
```

> **Neden Rust + CGo?** IgnisDB-Go, yazma yolundaki sıralı haritayı (SortedMap) ve Bloom
> filtre hash'ini Rust'a devreder. Bunlar `rust-core/` altında statik bir kütüphane derlenir
> ve Go tarafı CGo FFI ile çağırır. Bu yüzden `go build` tek başına yetmez; önce Rust çekirdeği
> derlenmiş olmalı.

---

## 1. Derleme

### Yönteme A — `make` (önerilen)

`make`, Rust çekirdeğini otomatik derleyip sonra Go binary'sini oluşturur:

```bash
git clone https://github.com/buraksocial/IgnisDB-Go.git
cd IgnisDB-Go
make build          # → rust-core: cargo build --release  +  go build ./cmd/server
```

Çıktı: `bin/ignisdb-go`

### Yönteme B — manuel

```bash
# 1) Rust statik kütüphanesi
cd rust-core && cargo build --release && cd ..

# 2) Go sunucusu (CGo linker libignisdb_core.a'yı bulmalı)
CGO_ENABLED=1 go build -ldflags="-s -w" -o bin/ignisdb-go ./cmd/server
```

### Yönteme C — örnek / test / bench

```bash
make test           # go test -v -race ./...  (Rust çekirdek önce derlenir)
make bench          # go test -bench=. -benchmem ./benchmarks/
```

---

## 2. Sunucuyu Çalıştırma

```bash
./bin/ignisdb-go --data-dir ./data --host 127.0.0.1 --port 6380
```

| Flag | Varsayılan | Açıklama |
|------|-----------|----------|
| `--data-dir` | `./data` | SSTable + WAL dizini |
| `--host` | `127.0.0.1` | Bind adresi |
| `--port` | `6380` | RESP protokol portu (Redis 6379'dan farklı seçildi) |

Günlük satırları yalnızca `IGNISDB_DEBUG=1` iken basılır (benchmark/performans kirlenmesin).

```bash
IGNISDB_DEBUG=1 ./bin/ignisdb-go   # sessiz modu aç, flush/compaction olaylarını logla
```

Kapatmak: `Ctrl-C` → sinyal yakalayıcı `srv.Stop()` + `eng.Close()` çağırır, MemTable disk'e
flush edilir, WAL kapanır.

---

## 3. İlk Veri — `redis-cli` ile

IgnisDB-Go RESP protokolünü konuştuğu için herhangi bir Redis istemcisiyle çalışır:

```bash
redis-cli -p 6380
127.0.0.1:6380> PING
PONG
127.0.0.1:6380> SET user:1 "Ada Lovelace"
OK
127.0.0.1:6380> GET user:1
"Ada Lovelace"
127.0.0.1:6380> SET session:abc tok123 EX 300   # 300 sn TTL
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

`redis-cli` yoksa ham TCP ile de deneyebilirsin:

```bash
printf '*2\r\n$4\r\nPING\r\n' | nc -q1 127.0.0.1 6380
# +PONG
```

Tüm komutların sözdizimi → [server-protocol_TR.md](server-protocol_TR.md)

---

## 4. İlk Veri — Gömülü (embedded) Go

Sunucuya ihtiyaç duymadan, uygulamana motoru doğrudan gömebilirsin:

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

    eng.Put([]byte("merhaba"), []byte("dünya"))
    if v, ok := eng.Get([]byte("merhaba")); ok {
        fmt.Printf("merhaba => %s\n", v) // merhaba => dünya
    }
}
```

> **Not:** `internal/` paketi yalnızca aynı modül içinden import edilebilir. Dış bir
> uygulamada kullanmak için ya kendi modülüne `replace` direktifi ekle, ya da sunucu +
> [pkg/client](embedded-usage_TR.md#istemci-go-client-kütüphanesi) istemcisini tercih et.

---

## 5. Kalıcılığı Doğrulama

Motor LSM-Tree'dir; yazılan veri önce WAL + MemTable'a gider, eşik aşılınca SSTable'a flush olur.
Güvenli kapatma (`Ctrl-C` / `eng.Close()`) kalan MemTable'ı flush eder. Sunucuyu yeniden
başlattığında SSTable + WAL replay ile veri geri gelir:

```bash
# yaz
./bin/ignisdb-go & sleep 1
redis-cli -p 6380 SET persist "evet"
kill -INT %1
# tekrar başlat ve oku
./bin/ignisdb-go & sleep 1
redis-cli -p 6380 GET persist   # => "evet"
```

---

## Sorun Giderme

| Belirti | Neden | Çözüm |
|---------|-------|-------|
| `undefined reference to ignis_engine_put` | Rust çekirdek derlenmemiş veya linker yolu yanlış | `cd rust-core && cargo build --release`, sonra `make build` |
| `build constraints exclude all Go files ... cgo` | `CGO_ENABLED=0` | `export CGO_ENABLED=1` |
| `C compiler gcc not found` | C toolchain yok | `apt install build-essential` |
| `engine not available at ... (rust core)` | Rust FFI init başarısız → Go'ya düşer | Normal; performans hibrit değil saf Go olur. `IGNISDB_DEBUG=1` ile logla |
| Port 6380 dolu | Başka instance | `--port 6381` ile farklı port kullan |

Bir sonraki adım: [embedded-usage_TR.md](embedded-usage_TR.md) → Go API'yi detaylı öğren.
