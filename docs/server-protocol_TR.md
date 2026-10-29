# Sunucu & RESP Protokolü

🌐 [English](server-protocol.md) · **Türkçe**

IgnisDB-Go, **RESP (REdis Serialization Protocol)** üzerinden konuşan bir TCP sunucusu
sunar. Bu sayede `redis-cli` ve çoğu Redis istemcisi doğrudan çalışır. Bu belge tüm
desteklenen komutları, sözdizimlerini ve gerçek davranışları listeler.

## Sunucuyu Başlatma

```bash
./bin/ignisdb-go --data-dir ./data --host 127.0.0.1 --port 6380
```

| Flag | Varsayılan | Açıklama |
|------|-----------|----------|
| `--data-dir` | `./data` | kalıcı veri dizini |
| `--host` | `127.0.0.1` | bind adresi |
| `--port` | `6380` | RESP portu |

Graceful shutdown: `SIGINT`/`SIGTERM` → sunucu durur, MemTable flush, WAL/SSTable kapanır.

---

## RESP Protocoli (kısa)

İstemci → sunucu **inline** veya **array** olarak gelir. Standart çoklu-argüman formatı:

```
*<arg_count>\r\n
$<len>\r\n<arg>\r\n
...
```

Sunucu yanıt türleri:

| Önek | Tür | Örnek |
|------|-----|-------|
| `+` | Simple String | `+OK\r\n` |
| `-` | Error | `-ERR unknown command\r\n` |
| `:` | Integer | `:(integer) 3\r\n` |
| `$` | Bulk String | `$5\r\nhello\r\n` (yoksa `$-1`) |
| `*` | Array | `*2\r\n...` |

Ham test (nc ile):

```bash
printf '*2\r\n$4\r\nPING\r\n$4\r\ntest\r\n' | nc -q1 127.0.0.1 6380
```

---

## Komut Referansı

### Strings (KV)

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `GET` | `GET key` | Bulk String veya nil |
| `SET` | `SET key value [EX sec] [PX ms]` | `+OK` |
| `DEL` / `DELETE` | `DEL key [key ...]` | Integer (silinen sayısı) |
| `EXISTS` | `EXISTS key` | Integer (0/1) |
| `EXPIRE` | `EXPIRE key seconds` | Integer (1 başarılı / 0 key yok) |
| `TTL` | `TTL key` | Integer (sn; `-1` sonsuz, `-2` yok) |
| `PERSIST` | `PERSIST key` | Integer (1 süre kaldırıldı / 0) |

**Notlar:**
- `SET ... EX s` / `PX ms` süreyi anahtara iliştirir.
- TTL **lazy expire** ile uygulanır: süre dolmuş anahtar `GET`/`TTL` anında silinir.
- `EXPIRE` yalnızca anahtar **zaten varsa** 1 döner.
- `DEL` silinse de silinmese de `count`'u artırır (motor tombstone yazar).

```
127.0.0.1:6380> SET token xyz EX 60
OK
127.0.0.1:6380> TTL token
(integer) 59
127.0.0.1:6380> PERSIST token
(integer) 1
127.0.0.1:6380> TTL token
(integer) -1
```

---

### Lists

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `LPUSH` | `LPUSH key value [value ...]` | Integer (yeni uzunluk) |
| `RPUSH` | `RPUSH key value [value ...]` | Integer (yeni uzunluk) |
| `LRANGE` | `LRANGE key start stop` | Array (Bulk String) |
| `LLEN` | `LLEN key` | Integer |

`start/stop` negatif olabilir (`-1` = son eleman). `LRANGE key 0 -1` tümünü verir.

```
127.0.0.1:6380> RPUSH mylist a b c
(integer) 3
127.0.0.1:6380> LPUSH mylist z
(integer) 4
127.0.0.1:6380> LRANGE mylist 0 -1
1) "z"
2) "a"
3) "b"
4) "c"
```

---

### Hashes

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `HSET` | `HSET key field value` | Integer (yeni alan sayısı) |
| `HGET` | `HGET key field` | Bulk String veya nil |
| `HGETALL` | `HGETALL key` | Array (`field1, value1, ...`) |
| `HDEL` | `HDEL key field [field ...]` | Integer (silinen alan sayısı) |

```
127.0.0.1:6380> HSET user:1 name Ada role engineer
(integer) 1        ← tek alanlık HSET çağrısı (Redis'in çoklu HSET'iyle fark)
127.0.0.1:6380> HGET user:1 name
"Ada"
127.0.0.1:6380> HGETALL user:1
1) "name"
2) "Ada"
3) "role"
4) "engineer"
```

---

### Sets

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `SADD` | `SADD key member [member ...]` | Integer (yeni üye sayısı) |
| `SMEMBERS` | `SMEMBERS key` | Array |
| `SISMEMBER` | `SISMEMBER key member` | Integer (0/1) |
| `SREM` | `SREM key member [member ...]` | Integer (silinen sayısı) |

---

### Sorted Sets (ZSET)

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `ZADD` | `ZADD key score member [score member ...]` | Integer (yeni üye) |
| `ZRANGE` | `ZRANGE key start stop` | Array |
| `ZREVRANGE` | `ZREVRANGE key start stop` | Array |
| `ZRANGEBYSCORE` | `ZRANGEBYSCORE key min max [LIMIT off cnt]` | Array |
| `ZREVRANGEBYSCORE` | `ZREVRANGEBYSCORE key max min [LIMIT off cnt]` | Array |
| `ZSCORE` | `ZSCORE key member` | Bulk String (float) veya nil |
| `ZREM` | `ZREM key member [member ...]` | Integer |
| `ZCARD` | `ZCARD key` | Integer |

`min`/`max` olarak `-inf`, `+inf`/`inf` veya sayı verilir.

```
127.0.0.1:6380> ZADD board 100 alice 200 bob 150 charlie
(integer) 3
127.0.0.1:6380> ZRANGE board 0 -1
1) "alice"
2) "charlie"
3) "bob"
127.0.0.1:6380> ZRANGEBYSCORE board 100 160
1) "alice"
2) "charlie"
127.0.0.1:6380> ZSCORE board bob
"200"
127.0.0.1:6380> ZCARD board
(integer) 3
```

---

### Full-Text Search

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `FT.ADD` | `FT.ADD doc_id text...` | `+OK` |
| `FT.SEARCH` | `FT.SEARCH query [LIMIT n]` | Array (`doc_id, score, ...`) |

Belge metni boşlukla bölünüp küçük harfe çevrilir, inverted index'e terim-frekansı olarak
eklenir. `FT.SEARCH` eşleşen belgeleri skora göre azalan sıralar.

```
127.0.0.1:6380> FT.ADD doc1 Go is a fast language
OK
127.0.0.1:6380> FT.ADD doc2 Go databases are performant
OK
127.0.0.1:6380> FT.SEARCH go LIMIT 10
1) "doc1"
2) "1"
3) "doc2"
4) "1"
```

> `FT.ADD` için argüman başlangıcından sonuna kadar tüm parçalar tek metin olarak birleştirilir.
> İndeks bellek-içi (in-memory) dur; SSTable'a yazılmaz (motor seviyesinde secondary index).

---

### Utility / Diagnostics

| Komut | Sözdizim | Yanıt |
|-------|----------|-------|
| `PING` | `PING [message]` | `+PONG` veya `message` |
| `INFO` | `INFO` | Bulk String (çok satırlı rapor) |
| `CACHE.STATS` | `CACHE.STATS` | Bulk String (`key: value` satırları) |
| `AUTH` | `AUTH [pass]` | `+OK` — **no-op** (kimlik doğrulama yok) |
| `MULTI` / `EXEC` / `DISCARD` | — | stub (transaction yok) |
| `SUBSCRIBE` / `PUBLISH` | — | stub (pub/sub yok) |

`INFO` örneği:

```
# Server
ignisdb_version:2.0.0-go
uptime_in_seconds:1730000000

# Storage
memtable_entries:512
sstable_count:2
total_writes:5000
total_reads:3200

# Cache
cache_hits:2800
cache_misses:400
cache_hit_rate:87.5%
cache_memory:41943040
```

---

## Desteklenmeyen Davranışlar

Şu Redis özellikleri **bilinçli olarak yok veya no-op'tur**:

- Gerçek **transaction/atomicity** (`MULTI/EXEC` OK döner ama birleştirmez).
- **Pub/Sub** (`SUBSCRIBE/PUBLISH` `+OK` döner, mesaj yayılmaz).
- **Kimlik doğrulama** (`AUTH` `+OK`, şifre kontrolü yok).
- **Cluster/replication**.
- `SCAN`, `KEYS`, `MSET`, `INCR` gibi ek komutlar.

Üretimde kimlik doğrulama için reverse-proxy (örn. TLS + auth) arkasına al.

## İlgili Belgeler

- Komutların Go karşılıkları (gömülü) → [embedded-usage_TR.md](embedded-usage_TR.md)
- Yanıt/mimari iç yüzü → [architecture_TR.md](architecture_TR.md)
