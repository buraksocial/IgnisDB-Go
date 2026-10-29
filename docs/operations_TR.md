# Operasyonlar

🌐 [English](operations.md) · **Türkçe**

IgnisDB-Go'yu üretimde çalıştırma: yaşam döngüsü, flush, compaction, kurtarma, izleme ve yedekleme.

## Süreç Yaşam Döngüsü

```bash
# ön planda
./bin/ignisdb-go --data-dir /var/lib/ignisdb --host 0.0.0.0 --port 6380

# systemd-dostu: SIGTERM/SIGINT ile durdur
kill -TERM <pid>
```

`SIGINT`/`SIGTERM`'de sunucu `Stop()` ardından `engine.Close()` çağırır:

1. aktif MemTable bir SSTable'a **flush edilir**,
2. tüm SSTable'lar kapatılır (mmap çözülür),
3. Rust SortedMap belleği serbest bırakılır,
4. WAL flush edilip kapatılır.

Her zaman sinyalle durdur — WAL replay'e güvenilmediği durumlar dışında yazma ortasında
`kill -9` yapma.

## systemd Ünitesi (taslak)

```ini
[Service]
ExecStart=/usr/local/bin/ignisdb-go --data-dir /var/lib/ignisdb --port 6380
Restart=on-failure
LimitNOFILE=65536
```

---

## Flush

Motor, MemTable boyutu `MemtableSize`'ı (varsayılan 16 MB) geçince **otomatik** flush yapar.
Temiz bir checkpoint için elle de zorlayabilirsin:

```go
eng.Flush()   // aktif MemTable'ı hemen SSTable'a yaz
```

Flush, sıcak yolu bloklamaz: immutable MemTable'ı `flushLoop` goroutine'ine devreder (kuyruk
doluysa tek seferlik flush başlatır).

---

## Compaction

Compaction **yalnızca arka planda** ve otomatiktir. SSTable sayısı `CompactionThreshold`'ı
(varsayılan 4) geçince, `compactLoop` tüm SSTable'ları tek sıralı dosyada birleştirir ve
tombstone'ları düşürür. **Public bir `Compact()` metodu yoktur**; stats üzerinden gözlemlenir.

Ayar rehberi:

| Durum | Değişiklik |
|-------|------------|
| Okuma amplifikasyonu yüksek (çok SSTable) | `CompactionThreshold` düşür |
| Yazma amplifikasyonu / I/O yüksek | `CompactionThreshold` ve `MemtableSize` yükselt |
| Yoğun silme (çok tombstone) | tombstone daha erken flush olsun diye `MemtableSize` düşür |

```go
stats := eng.GetStats()
fmt.Printf("şu ana kadar compaction: %v\n", stats["compactions"])
fmt.Printf("sstable_count: %v, total_bytes: %v\n", stats["sstable_count"], stats["sstable_total_bytes"])
```

---

## Crash Kurtarma

WAL, son buffered flush'a kadar dayanıklılığı garanti eder. `Open()` sırasında:

1. `loadSSTables()` her `sstable_*.db`'yi (sıralı) okur, sequence sayacını yeniden kurar,
2. `wal.Replay()` `WalPut`/`WalDelete` kayıtlarını MemTable'a yeniden uygular,
3. replay ilk CRC uyuşmazlığında durur (kısmi/kırpık yazma güvenle atılır).

WAL yazma buffer'ı **256 KB ve periyodik flush** olduğu için, çökme diske henüz inmemiş son
kayıtları kaybedebilir. Kayıt başına dayanıklılık gerekiyorsa `wal.Sync()` çağır (motor API'sinde
şu an dışa açık değil).

```
Open(dir)
  → loadSSTables  (sstable_*.db oku, sequence'ı geri yükle)
  → wal.OpenFile   (append modu)
  → replayWAL       (log'dan MemTable'ı kur, CRC doğrula)
```

Kurtarmayı, sunucuyu yeniden başlatıp bilinen bir anahtarı okuyarak doğrula (bkz.
[getting-started_TR.md](getting-started_TR.md#5-kalıcılığı-doğrulama)).

---

## İzleme

`engine.GetStats()` zengin bir map döner:

| Anahtar | Neye bakılmalı |
|---------|----------------|
| `writes` / `reads` | verim trendi |
| `cache_hits` / `cache_misses` / `cache.hit_rate_pct` | hit oranı düşükse `CacheSize` yükselt |
| `sstable_reads` | diske okuma amplifikasyonu |
| `compactions` | zamanla artmalı; tıkanma I/O doygunluğuna işaret |
| `memtable_entries` / `memtable_bytes` | `MemtableSize`'a yaklaşınca flush baskısı |
| `wal_size_bytes` | hep büyüyorsa flush yetişemiyor |
| `sstable_total_bytes` | disk ayak izi |
| `rust_core_active` | `true` olmalı; `false` sessiz Go fallback (daha yavaş) demek |

Sunucu aynılarını RESP ile dışa verir:

```
127.0.0.1:6380> INFO
127.0.0.1:6380> CACHE.STATS
```

**Debug loglama:** flush/compaction/WAL/replay olaylarını izlemek için `IGNISDB_DEBUG=1`.
Normal çalıştırmada log kirliliği için kapat.

---

## Yedekleme & Geri Yükleme

Veri dizini kendi kendine yeterlidir: SSTable'lar (`sstable_*.db`) + WAL (`wal.log`).

**Güvenli anlık kopya (snapshot):**
1. flush zorla (`eng.Flush()`; sunucu yolunda `FLUSH` komutu dışa açık değil — temiz yeniden
   başlatmayı veya düşük trafikte snapshot almayı tercih et),
2. süreci durdur (`SIGTERM`) ki WAL kapansın, veya
3. tüm `--data-dir`'yi kopyala.

**Geri yükleme:** dizini geri kopyala ve `Open()` — SSTable + WAL replay durumu yeniden kurar.

> Henüz artımlı/online yedek yok. Sıcak yedek için durdur-kopyala veya flush + `wal.Sync()`
> kanca ekle.

---

## Sık İşlemler

| Görev | Nasıl |
|-------|-------|
| Port/host değiştir | `--port` / `--host` flag'leri |
| RAM ayak izini düşür | `MemtableSize` / `CacheSize` düşür (gömülü) |
| Kalıcı yazmaları zorla | periyodik `wal.Sync()` çağır (gömülü) |
| Rust yolunun aktif olduğunu doğrula | `GetStats()["rust_core_active"]` |
| Diski incele | `ls -l $DATA_DIR` → `sstable_*.db`, `wal.log` |

---

## Sorun Giderme

| Belirti | Muhtemel neden | Aksiyon |
|---------|----------------|---------|
| `wal_size_bytes` yüksek, hiç küçülmüyor | flush/compaction yetişemiyor | disk I/O önceliğini yükselt, `MemtableSize` düşür |
| Düşük cache hit oranı | `CacheSize` hot set için küçük | `CacheSize` yükselt |
| `rust_core_active:false` | Rust lib linklenmemiş / init başarısız | `make build` ile yeniden derle; `CGO_ENABLED=1` kontrol et |
| Çökme sonrası veri eksik | son buffered WAL diske inmemiş | periyodik `Sync` ekle; yazma batch'ini azalt |
| Compaction tıkanması | tek dev SSTable merge I/O-bound | patlamalarla `CompactionThreshold` yükselterek işi böl |
| Gecikme sıçramaları | flush sırasında Go GC | `GOGC` ayarla; buffer'ları yeniden kullan |

## İlgili

- Mimari iç yüzü → [architecture_TR.md](architecture_TR.md)
- Ayar değerleri → [configuration_TR.md](configuration_TR.md)
