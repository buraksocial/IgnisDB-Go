# Benchmarklar

🌐 [English](benchmarks.md) · **Türkçe**

Performansın nasıl ölçüldüğü, referans donanımdaki sayılar ve bunların nasıl yeniden üretileceği.

## Metodoloji

Benchmarklar `benchmarks/` altındaki Go `testing` takımdır. **Gömülü motoru** ölçerler
(ağ yok, RESP yok); böylece sayılar depolama sıcak yolunu izole eder.

Her `ns/op` **tek goroutine'li sıralı gecikmedir**; ops/s = `1e9 / ns_per_op` olarak türetilir.
Go bunları varsayılan olarak tek goroutine'de çalıştırır; yani bir işlem başına maliyeti
yansıtır, paralel toplam verimi değil.

| Benchmark | Ne yapar | Kurulum |
|-----------|----------|---------|
| `BenchmarkWrite` | 10,000 kayıt warmup, sonra sürekli `Put` | `key:N` / `value:N`, havuzlu buffer'lar |
| `BenchmarkReadHot` | 10,000 anahtar doldur, sonra aralarında `Get` | veri memtable'da kalır → cache hit |
| `BenchmarkReadCold` | 100,000 anahtar doldur, SSTable'a `Flush`, sonra `Get` | SSTable/bloom yolunu zorlar |
| `BenchmarkMixedWorkload` | 10,000 kümede %10 yazma / %90 okuma | gerçekçi karışık erişim |

**Allocation hijyeni:** anahtar/değerler özel `appendInt` ile üretilir (`strconv.Itoa` yok),
ve 10,000 kayıt warmup `sync.Pool` önbelleklerini doldurur; zamanlayıcı steady-state ölçer.

## Nasıl Çalıştırılır

```bash
make bench          # önce Rust çekirdeği derler, sonra:
# CGO_ENABLED=1 go test -bench=. -benchmem ./benchmarks/

# Daha uzun, daha stabil ölçüm:
CGO_ENABLED=1 go test -bench=. -benchmem -benchtime=3s -count=5 ./benchmarks/
```

## Referans Sonuçları

**13th Gen Intel i5-13500H**, `CGO_ENABLED=1`, hibrit Rust çekirdeği aktif, `-benchtime=3s`
ile ölçüldü. Dört benchmark da **~1 alloc/op** bildirir.

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

## Python v2 ile Karşılaştırma

Python referansı (IgnisDB v2) benzer donanımda kendi takımını çalıştırdı:

| İşlem | Python v2 | IgnisDB-Go (Hybrid) | Hızlanma |
|-------|-----------|---------------------|----------|
| Write | 48,543 ops/s | ~7,587,000 ops/s | **~156x** |
| Read (hot) | 125,786 ops/s | ~5,592,800 ops/s | **~44x** |
| Read (cold) | 28,409 ops/s | ~4,967,700 ops/s | **~175x** |
| Mixed | 108,696 ops/s | ~4,058,400 ops/s | **~37x** |

En büyük kazanç **write** ve **read-cold**'da; Go'nun mmap + bloom + havuzlu buffer'ları
saf Python yolunu ezerek geçer.

## Mimari Evrim (hibrit neden kazanır)

| Varyant | Write ns/op | ReadHot ns/op | Hot allocs |
|---------|-------------|---------------|------------|
| Python v2 | ~20,600 | ~7,950 | — |
| Go (saf / hibrit sıcak yol) | **~117–132** | **~65–179** | **0–1** |
| Go + **Full Rust Core** | ~1,042 | ~454 | 3–4 |
| Go + **Rust Hybrid (mevcut)** | ~132 | ~179 | ~1 |

Tam-Rust denemesi, her işlemin CGo sınırını geçmesi (~1µs/op) nedeniyle regresyona uğradı.
Hibrit tasarıma (Rust yalnızca SortedMap/Bloom için, gerisi Go) geri dönmek, near-zero
allocation'ı ve 5–8x daha iyi gecikmeyi geri getirdi. Bkz. [architecture_TR.md](architecture_TR.md).

## Notlar

- Bunlar **motor-seviyesi** sayılardır. Uçtan-uca sunucu verimi RESP ayrıştırma + TCP ekler,
  daha düşüktür ve bağlantı sayısı ile değer boyutlarına bağlıdır.
- `read cold` diske flush edilmiş 100k anahtar kümesi kullanır; sonuçlar SSTable sayısı ve OS
  page cache sıcaklığına göre değişir.
- ~1/op alloc, pool warmup sonrası nadir memtable node/WAL buffer büyümesinden gelir; anahtar
  yol için steady-state fiilen allocation-free'dir.

## İlgili

- Hızın kaynağı → [architecture_TR.md](architecture_TR.md)
- Buffer/cache ayarı → [configuration_TR.md](configuration_TR.md)
