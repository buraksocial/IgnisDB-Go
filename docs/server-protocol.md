# Server & RESP Protocol

🌐 **English** · [Türkçe](server-protocol_TR.md)

IgnisDB-Go provides a TCP server that speaks **RESP (REdis Serialization Protocol)**. This
makes `redis-cli` and most Redis clients work out of the box. This document lists every
supported command, its syntax, and its actual behavior.

## Starting the Server

```bash
./bin/ignisdb-go --data-dir ./data --host 127.0.0.1 --port 6380
```

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `./data` | persistent data directory |
| `--host` | `127.0.0.1` | TCP bind address |
| `--port` | `6380` | RESP port |

Graceful shutdown: `SIGINT`/`SIGTERM` → the server stops, the MemTable is flushed, and the
WAL/SSTables are closed.

---

## RESP Protocol (brief)

Clients send requests as **inline** or as a **multi-bulk array**. Standard multi-argument format:

```
*<arg_count>\r\n
$<len>\r\n<arg>\r\n
...
```

Server reply types:

| Prefix | Type | Example |
|--------|------|---------|
| `+` | Simple String | `+OK\r\n` |
| `-` | Error | `-ERR unknown command\r\n` |
| `:` | Integer | `:(integer) 3\r\n` |
| `$` | Bulk String | `$5\r\nhello\r\n` (nil is `$-1`) |
| `*` | Array | `*2\r\n...` |

Raw test (with nc):

```bash
printf '*2\r\n$4\r\nPING\r\n$4\r\ntest\r\n' | nc -q1 127.0.0.1 6380
```

---

## Command Reference

### Strings (KV)

| Command | Syntax | Reply |
|---------|--------|-------|
| `GET` | `GET key` | Bulk String or nil |
| `SET` | `SET key value [EX sec] [PX ms]` | `+OK` |
| `DEL` / `DELETE` | `DEL key [key ...]` | Integer (number deleted) |
| `EXISTS` | `EXISTS key` | Integer (0/1) |
| `EXPIRE` | `EXPIRE key seconds` | Integer (1 success / 0 key absent) |
| `TTL` | `TTL key` | Integer (seconds; `-1` persistent, `-2` absent) |
| `PERSIST` | `PERSIST key` | Integer (1 removed / 0) |

**Notes:**
- `SET ... EX s` / `PX ms` attaches a TTL to the key.
- TTL is applied via **lazy expire**: an expired key is deleted at the moment of `GET`/`TTL`.
- `EXPIRE` returns 1 only if the key **already exists**.
- `DEL` increments `count` even if a key wasn't present (the engine writes a tombstone).

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

| Command | Syntax | Reply |
|---------|--------|-------|
| `LPUSH` | `LPUSH key value [value ...]` | Integer (new length) |
| `RPUSH` | `RPUSH key value [value ...]` | Integer (new length) |
| `LRANGE` | `LRANGE key start stop` | Array (Bulk Strings) |
| `LLEN` | `LLEN key` | Integer |

`start/stop` may be negative (`-1` = last element). `LRANGE key 0 -1` returns everything.

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

| Command | Syntax | Reply |
|---------|--------|-------|
| `HSET` | `HSET key field value` | Integer (whether the field is new) |
| `HGET` | `HGET key field` | Bulk String or nil |
| `HGETALL` | `HGETALL key` | Array (`field1, value1, ...`) |
| `HDEL` | `HDEL key field [field ...]` | Integer (number of fields removed) |

```
127.0.0.1:6380> HSET user:1 name Ada
(integer) 1
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

| Command | Syntax | Reply |
|---------|--------|-------|
| `SADD` | `SADD key member [member ...]` | Integer (number added) |
| `SMEMBERS` | `SMEMBERS key` | Array |
| `SISMEMBER` | `SISMEMBER key member` | Integer (0/1) |
| `SREM` | `SREM key member [member ...]` | Integer (number removed) |

---

### Sorted Sets (ZSET)

| Command | Syntax | Reply |
|---------|--------|-------|
| `ZADD` | `ZADD key score member [score member ...]` | Integer (number added) |
| `ZRANGE` | `ZRANGE key start stop` | Array |
| `ZREVRANGE` | `ZREVRANGE key start stop` | Array |
| `ZRANGEBYSCORE` | `ZRANGEBYSCORE key min max [LIMIT off cnt]` | Array |
| `ZREVRANGEBYSCORE` | `ZREVRANGEBYSCORE key max min [LIMIT off cnt]` | Array |
| `ZSCORE` | `ZSCORE key member` | Bulk String (float) or nil |
| `ZREM` | `ZREM key member [member ...]` | Integer |
| `ZCARD` | `ZCARD key` | Integer |

`min`/`max` accept `-inf`, `+inf`/`inf`, or a number.

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

| Command | Syntax | Reply |
|---------|--------|-------|
| `FT.ADD` | `FT.ADD doc_id text...` | `+OK` |
| `FT.SEARCH` | `FT.SEARCH query [LIMIT n]` | Array (`doc_id, score, ...`) |

The document text is split on whitespace, lower-cased, and added to the inverted index as
term frequency. `FT.SEARCH` returns matching documents ordered by descending score.

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

> For `FT.ADD`, every argument from the second onward is joined into one text. The index is
> held in memory (a secondary structure at the engine level); it is not written to SSTables.

---

### Utility / Diagnostics

| Command | Syntax | Reply |
|---------|--------|-------|
| `PING` | `PING [message]` | `+PONG` or `message` |
| `INFO` | `INFO` | Bulk String (multi-line report) |
| `CACHE.STATS` | `CACHE.STATS` | Bulk String (`key: value` lines) |
| `AUTH` | `AUTH [pass]` | `+OK` — **no-op** (no authentication) |
| `MULTI` / `EXEC` / `DISCARD` | — | stub (no transactions) |
| `SUBSCRIBE` / `PUBLISH` | — | stub (no pub/sub) |

`INFO` example:

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

## Unsupported Behavior

The following Redis features are **intentionally absent or no-ops**:

- Real **transactions/atomicity** (`MULTI/EXEC` reply OK but do not batch).
- **Pub/Sub** (`SUBSCRIBE/PUBLISH` reply `+OK`; no message is delivered).
- **Authentication** (`AUTH` replies `+OK`; no password check).
- **Cluster/replication**.
- Additional commands such as `SCAN`, `KEYS`, `MSET`, `INCR`.

For production authentication, place IgnisDB-Go behind a reverse proxy (e.g. TLS + auth).

## Related Docs

- Go counterparts of the commands (embedded) → [embedded-usage.md](embedded-usage.md)
- Reply/architecture internals → [architecture.md](architecture.md)
