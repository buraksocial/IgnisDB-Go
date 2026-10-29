//! IgnisDB Rust Core - Complete LSM-Tree Storage Engine
//!
//! Full storage engine in Rust exposed via C FFI for Go CGo integration.
//! Includes: MemTable (BTreeMap), WAL, SSTable (mmap), LRU Cache, Bloom Filter, Compaction.

use std::cell::UnsafeCell;
use std::collections::{BTreeMap, HashMap, VecDeque};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Write, BufWriter, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, atomic::{AtomicU64, Ordering}};

// ============================================================
// Fast Hash (xxhash3-inspired)
// ============================================================

#[inline]
fn fast_hash(data: &[u8], seed: u64) -> u64 {
    const P1: u64 = 0x9E3779B185EBCA87;
    const P2: u64 = 0xC2B2AE3D27D4EB4F;
    const P3: u64 = 0x165667B19E3779F9;
    let mut h = seed ^ P1;
    let len = data.len();
    let mut i = 0;
    while i + 8 <= len {
        let k = u64::from_le_bytes(data[i..i+8].try_into().unwrap());
        let k = k.wrapping_mul(P2).rotate_left(31).wrapping_mul(P3);
        h ^= k;
        h = h.rotate_left(27).wrapping_mul(P1);
        i += 8;
    }
    while i < len {
        h ^= (data[i] as u64).wrapping_mul(P3);
        h = h.rotate_left(11).wrapping_mul(P1);
        i += 1;
    }
    h ^= len as u64;
    h ^= h >> 33; h = h.wrapping_mul(P2);
    h ^= h >> 29; h = h.wrapping_mul(P3);
    h ^= h >> 32;
    h
}

// ============================================================
// Bloom Filter
// ============================================================

struct BloomFilter {
    bits: Vec<u8>,
    size: usize,
    hash_count: usize,
}

impl BloomFilter {
    fn new(expected: usize, fp_rate: f64) -> Self {
        let size = if expected == 0 { 64 } else {
            ((-(expected as f64) * fp_rate.ln() / (2.0_f64.ln().powi(2))) as usize).max(64)
        };
        let hash_count = if expected == 0 { 1 } else {
            (((size as f64 / expected as f64) * 2.0_f64.ln()) as usize).max(1)
        };
        BloomFilter { bits: vec![0u8; size / 8 + 1], size, hash_count }
    }

    fn from_keys(keys: &[&[u8]], fp_rate: f64) -> Self {
        let mut bf = Self::new(keys.len(), fp_rate);
        for k in keys { bf.add(k); }
        bf
    }

    #[inline]
    fn add(&mut self, key: &[u8]) {
        let h1 = fast_hash(key, 0);
        let h2 = fast_hash(key, h1);
        for i in 0..self.hash_count {
            let pos = (h1.wrapping_add((i as u64).wrapping_mul(h2))) % (self.size as u64);
            self.bits[pos as usize / 8] |= 1 << (pos as usize % 8);
        }
    }

    #[inline]
    fn might_contain(&self, key: &[u8]) -> bool {
        let h1 = fast_hash(key, 0);
        let h2 = fast_hash(key, h1);
        for i in 0..self.hash_count {
            let pos = (h1.wrapping_add((i as u64).wrapping_mul(h2))) % (self.size as u64);
            if self.bits[pos as usize / 8] & (1 << (pos as usize % 8)) == 0 { return false; }
        }
        true
    }

    fn serialize(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(12 + self.bits.len());
        buf.extend_from_slice(&(self.size as u32).to_le_bytes());
        buf.extend_from_slice(&(self.hash_count as u32).to_le_bytes());
        buf.extend_from_slice(&0u32.to_le_bytes()); // count placeholder
        buf.extend_from_slice(&self.bits);
        buf
    }

    fn deserialize(data: &[u8]) -> Self {
        if data.len() < 12 { return Self::new(100, 0.01); }
        let size = u32::from_le_bytes(data[0..4].try_into().unwrap()) as usize;
        let hash_count = u32::from_le_bytes(data[4..8].try_into().unwrap()) as usize;
        BloomFilter { bits: data[12..].to_vec(), size, hash_count }
    }
}

// ============================================================
// LRU Cache
// ============================================================

struct LRUCache {
    max_bytes: usize,
    current_size: usize,
    map: HashMap<Vec<u8>, (Vec<u8>, usize)>, // key -> (value, order)
    order: VecDeque<Vec<u8>>,
    hits: u64,
    misses: u64,
}

impl LRUCache {
    fn new(max_bytes: usize) -> Self {
        LRUCache {
            max_bytes, current_size: 0,
            map: HashMap::new(), order: VecDeque::new(),
            hits: 0, misses: 0,
        }
    }

    fn get(&mut self, key: &[u8]) -> Option<Vec<u8>> {
        if let Some((value, _)) = self.map.get(key) {
            self.hits += 1;
            // Move to back (most recent)
            self.order.retain(|k| k.as_slice() != key);
            self.order.push_back(key.to_vec());
            Some(value.clone())
        } else {
            self.misses += 1;
            None
        }
    }

    fn put(&mut self, key: &[u8], value: &[u8]) {
        let entry_size = key.len() + value.len();
        if entry_size > self.max_bytes / 10 { return; }

        // Remove old entry if exists
        if self.map.contains_key(key) {
            let old_size = key.len() + self.map[key].0.len();
            self.current_size -= old_size;
            self.order.retain(|k| k.as_slice() != key);
            self.map.remove(key);
        }

        // Evict until space available
        while self.current_size + entry_size > self.max_bytes && !self.order.is_empty() {
            if let Some(evict_key) = self.order.pop_front() {
                if let Some((evict_val, _)) = self.map.remove(&evict_key) {
                    self.current_size -= evict_key.len() + evict_val.len();
                }
            }
        }

        self.map.insert(key.to_vec(), (value.to_vec(), 0));
        self.order.push_back(key.to_vec());
        self.current_size += entry_size;
    }

    fn delete(&mut self, key: &[u8]) {
        if let Some((val, _)) = self.map.remove(key) {
            self.current_size -= key.len() + val.len();
            self.order.retain(|k| k.as_slice() != key);
        }
    }
}

// ============================================================
// WAL (Write-Ahead Log)
// ============================================================

const WAL_PUT: u8 = 1;
const WAL_DELETE: u8 = 2;

struct WalEntry {
    entry_type: u8,
    key: Vec<u8>,
    value: Vec<u8>,
}

struct WAL {
    path: PathBuf,
    writer: Option<BufWriter<File>>,
}

impl WAL {
    fn open(data_dir: &Path) -> io::Result<Self> {
        let path = data_dir.join("wal.log");
        let file = OpenOptions::new().create(true).append(true).open(&path)?;
        Ok(WAL { path, writer: Some(BufWriter::with_capacity(256 * 1024, file)) })
    }

    fn append(&mut self, key: &[u8], value: &[u8], entry_type: u8) -> io::Result<()> {
        let w = self.writer.as_mut().unwrap();
        // type(1) + key_len(4) + key + value_len(4) + value
        let mut entry_data = Vec::with_capacity(1 + 4 + key.len() + 4 + value.len());
        entry_data.push(entry_type);
        entry_data.extend_from_slice(&(key.len() as u32).to_le_bytes());
        entry_data.extend_from_slice(key);
        entry_data.extend_from_slice(&(value.len() as u32).to_le_bytes());
        entry_data.extend_from_slice(value);
        let checksum = crc32fast::hash(&entry_data);
        w.write_all(&entry_data)?;
        w.write_all(&checksum.to_le_bytes())?;
        Ok(())
    }

    fn sync(&mut self) -> io::Result<()> {
        if let Some(w) = self.writer.as_mut() { w.flush()?; }
        Ok(())
    }

    fn truncate(&mut self) -> io::Result<()> {
        drop(self.writer.take());
        let old_path = self.path.with_extension("old");
        if self.path.exists() { fs::rename(&self.path, &old_path).ok(); }
        let file = OpenOptions::new().create(true).write(true).truncate(true).open(&self.path)?;
        self.writer = Some(BufWriter::with_capacity(256 * 1024, file));
        fs::remove_file(old_path).ok();
        Ok(())
    }

    fn replay(path: &Path) -> Vec<WalEntry> {
        let mut entries = Vec::new();
        let data = match fs::read(path) { Ok(d) => d, Err(_) => return entries };
        let mut pos = 0;
        while pos < data.len() {
            if pos + 1 > data.len() { break; }
            let entry_type = data[pos]; pos += 1;
            if pos + 4 > data.len() { break; }
            let key_len = u32::from_le_bytes(data[pos..pos+4].try_into().unwrap()) as usize; pos += 4;
            if key_len > 10 * 1024 * 1024 || pos + key_len > data.len() { break; }
            let key = data[pos..pos+key_len].to_vec(); pos += key_len;
            if pos + 4 > data.len() { break; }
            let val_len = u32::from_le_bytes(data[pos..pos+4].try_into().unwrap()) as usize; pos += 4;
            if pos + val_len > data.len() { break; }
            let value = data[pos..pos+val_len].to_vec(); pos += val_len;
            if pos + 4 > data.len() { break; }
            let stored_crc = u32::from_le_bytes(data[pos..pos+4].try_into().unwrap()); pos += 4;
            // Verify checksum
            let mut entry_data = Vec::with_capacity(1 + 4 + key_len + 4 + val_len);
            entry_data.push(entry_type);
            entry_data.extend_from_slice(&(key_len as u32).to_le_bytes());
            entry_data.extend_from_slice(&key);
            entry_data.extend_from_slice(&(val_len as u32).to_le_bytes());
            entry_data.extend_from_slice(&value);
            if crc32fast::hash(&entry_data) != stored_crc { break; }
            entries.push(WalEntry { entry_type, key, value });
        }
        entries
    }

    fn close(&mut self) -> io::Result<()> {
        if let Some(mut w) = self.writer.take() { w.flush()?; }
        Ok(())
    }
}

// ============================================================
// SSTable
// ============================================================

const MAGIC: &[u8; 4] = b"IGDB";
const VERSION: u16 = 1;
const BLOCK_SIZE: usize = 4096;
const HEADER_SIZE: usize = 16;
const FOOTER_SIZE: usize = 16;

struct SSTableMeta {
    path: PathBuf,
    entry_count: u32,
    index_offset: u64,
    bloom_offset: u64,
    bloom: BloomFilter,
    index: Vec<(Vec<u8>, u64)>, // (first_key, offset)
}

impl SSTableMeta {
    fn create(entries: &[(Vec<u8>, Vec<u8>)], data_dir: &Path, seq: u64) -> io::Result<PathBuf> {
        let path = data_dir.join(format!("sstable_{:016}.db", seq));
        let mut f = BufWriter::new(File::create(&path)?);

        // Header
        f.write_all(MAGIC)?;
        f.write_all(&VERSION.to_le_bytes())?;
        f.write_all(&(entries.len() as u32).to_le_bytes())?;
        f.write_all(&[0u8; 6])?; // reserved

        // Data blocks
        let mut index_entries: Vec<(Vec<u8>, u64)> = Vec::new();
        let mut bytes_since_index = 0usize;
        for (i, (key, value)) in entries.iter().enumerate() {
            let offset = f.seek(SeekFrom::Current(0))?;
            if bytes_since_index >= BLOCK_SIZE || i == 0 {
                index_entries.push((key.clone(), offset));
                bytes_since_index = 0;
            }
            f.write_all(&(key.len() as u32).to_le_bytes())?;
            f.write_all(key)?;
            f.write_all(&(value.len() as u32).to_le_bytes())?;
            f.write_all(value)?;
            bytes_since_index += 4 + key.len() + 4 + value.len();
        }

        // Index block
        let index_offset = f.seek(SeekFrom::Current(0))?;
        for (key, offset) in &index_entries {
            f.write_all(&(key.len() as u32).to_le_bytes())?;
            f.write_all(key)?;
            f.write_all(&(*offset as u64).to_le_bytes())?;
        }

        // Bloom filter
        let bloom_offset = f.seek(SeekFrom::Current(0))?;
        let keys: Vec<&[u8]> = entries.iter().map(|(k, _)| k.as_slice()).collect();
        let bloom = BloomFilter::from_keys(&keys, 0.01);
        let bloom_data = bloom.serialize();
        f.write_all(&bloom_data)?;

        // Footer
        f.write_all(&(index_offset as u64).to_le_bytes())?;
        f.write_all(&(bloom_offset as u64).to_le_bytes())?;

        f.flush()?;
        Ok(path)
    }

    fn open(path: &Path) -> io::Result<Self> {
        let data = fs::read(path)?;
        if data.len() < HEADER_SIZE + FOOTER_SIZE { return Err(io::Error::new(io::ErrorKind::InvalidData, "too small")); }
        if &data[0..4] != MAGIC { return Err(io::Error::new(io::ErrorKind::InvalidData, "bad magic")); }

        let entry_count = u32::from_le_bytes(data[6..10].try_into().unwrap());
        let footer_start = data.len() - FOOTER_SIZE;
        let index_offset = u64::from_le_bytes(data[footer_start..footer_start+8].try_into().unwrap());
        let bloom_offset = u64::from_le_bytes(data[footer_start+8..footer_start+16].try_into().unwrap());

        let bloom = if bloom_offset > 0 && (bloom_offset as usize) < footer_start {
            BloomFilter::deserialize(&data[bloom_offset as usize..footer_start])
        } else {
            BloomFilter::new(100, 0.01)
        };

        // Parse sparse index
        let mut index = Vec::new();
        let mut pos = index_offset as usize;
        let bloom_start = bloom_offset as usize;
        while pos + 4 <= bloom_start {
            let key_len = u32::from_le_bytes(data[pos..pos+4].try_into().unwrap()) as usize;
            pos += 4;
            if pos + key_len + 8 > bloom_start { break; }
            let key = data[pos..pos+key_len].to_vec();
            pos += key_len;
            let offset = u64::from_le_bytes(data[pos..pos+8].try_into().unwrap());
            pos += 8;
            index.push((key, offset));
        }

        Ok(SSTableMeta { path: path.to_path_buf(), entry_count, index_offset, bloom_offset, bloom, index })
    }

    fn get(&self, key: &[u8], mmap: &[u8]) -> Option<Vec<u8>> {
        if !self.bloom.might_contain(key) { return None; }

        // Binary search in sparse index
        let start_offset = if self.index.is_empty() { HEADER_SIZE as u64 } else {
            let mut lo = 0; let mut hi = self.index.len();
            while lo < hi {
                let mid = (lo + hi) / 2;
                if self.index[mid].0.as_slice() <= key { lo = mid + 1; } else { hi = mid; }
            }
            if lo == 0 { HEADER_SIZE as u64 } else { self.index[lo - 1].1 }
        };

        // Linear scan within block
        let mut pos = start_offset as usize;
        while pos < self.index_offset as usize {
            if pos + 4 > mmap.len() { break; }
            let key_len = u32::from_le_bytes(mmap[pos..pos+4].try_into().unwrap()) as usize;
            if key_len == 0 || key_len > 10 * 1024 * 1024 { break; }
            pos += 4;
            if pos + key_len > mmap.len() { break; }
            let current_key = &mmap[pos..pos+key_len];
            pos += key_len;
            if pos + 4 > mmap.len() { break; }
            let val_len = u32::from_le_bytes(mmap[pos..pos+4].try_into().unwrap()) as usize;
            pos += 4;

            if current_key == key {
                if pos + val_len > mmap.len() { break; }
                return Some(mmap[pos..pos+val_len].to_vec());
            } else if current_key > key {
                break;
            }
            pos += val_len;
        }
        None
    }

    fn iter_all(&self, mmap: &[u8]) -> Vec<(Vec<u8>, Vec<u8>)> {
        let mut entries = Vec::with_capacity(self.entry_count as usize);
        let mut pos = HEADER_SIZE;
        for _ in 0..self.entry_count {
            if pos + 4 > mmap.len() { break; }
            let key_len = u32::from_le_bytes(mmap[pos..pos+4].try_into().unwrap()) as usize;
            pos += 4;
            if pos + key_len > mmap.len() { break; }
            let key = mmap[pos..pos+key_len].to_vec();
            pos += key_len;
            if pos + 4 > mmap.len() { break; }
            let val_len = u32::from_le_bytes(mmap[pos..pos+4].try_into().unwrap()) as usize;
            pos += 4;
            if pos + val_len > mmap.len() { break; }
            let value = mmap[pos..pos+val_len].to_vec();
            pos += val_len;
            entries.push((key, value));
        }
        entries
    }
}

// ============================================================
// LSM Engine (Complete)
// ============================================================

pub struct LSMEngine {
    data_dir: PathBuf,
    memtable: UnsafeCell<BTreeMap<Vec<u8>, Vec<u8>>>,
    memtable_size: AtomicU64,
    sstables: UnsafeCell<Vec<(SSTableMeta, Arc<Vec<u8>>)>>,
    wal: Mutex<WAL>,
    cache: Mutex<LRUCache>,
    sequence: AtomicU64,
    stats_writes: AtomicU64,
    stats_reads: AtomicU64,
    stats_cache_hits: AtomicU64,
    stats_cache_misses: AtomicU64,
    memtable_size_limit: u64,
}

impl LSMEngine {
    fn open(data_dir: &str, memtable_size_limit: u64, cache_size: usize) -> io::Result<Self> {
        let data_dir = PathBuf::from(data_dir);
        fs::create_dir_all(&data_dir)?;

        let mut engine = LSMEngine {
            data_dir: data_dir.clone(),
            memtable: UnsafeCell::new(BTreeMap::new()),
            memtable_size: AtomicU64::new(0),
            sstables: UnsafeCell::new(Vec::new()),
            wal: Mutex::new(WAL::open(&data_dir)?),
            cache: Mutex::new(LRUCache::new(cache_size)),
            sequence: AtomicU64::new(0),
            stats_writes: AtomicU64::new(0),
            stats_reads: AtomicU64::new(0),
            stats_cache_hits: AtomicU64::new(0),
            stats_cache_misses: AtomicU64::new(0),
            memtable_size_limit,
        };

        // Load existing SSTables
        engine.load_sstables()?;

        // Replay WAL
        let wal_path = data_dir.join("wal.log");
        let entries = WAL::replay(&wal_path);
        let mt = unsafe { &mut *engine.memtable.get() };
        for e in entries {
            match e.entry_type {
                WAL_PUT => {
                    engine.memtable_size.fetch_add((e.key.len() + e.value.len()) as u64, Ordering::Relaxed);
                    mt.insert(e.key, e.value);
                }
                WAL_DELETE => {
                    mt.insert(e.key, Vec::new());
                }
                _ => {}
            }
        }

        Ok(engine)
    }

    fn load_sstables(&mut self) -> io::Result<()> {
        let mut paths: Vec<PathBuf> = fs::read_dir(&self.data_dir)?
            .filter_map(|e| e.ok())
            .map(|e| e.path())
            .filter(|p| p.file_name().map_or(false, |n| n.to_string_lossy().starts_with("sstable_") && n.to_string_lossy().ends_with(".db")))
            .collect();
        paths.sort();

        for path in paths {
            match SSTableMeta::open(&path) {
                Ok(meta) => {
                    // Memory-map the file
                    let file = File::open(&path)?;
                    let mmap = unsafe { memmap2::Mmap::map(&file)? };
                    let data: Vec<u8> = mmap.to_vec(); // Copy to owned Vec for safety across FFI
                    // Extract sequence number
                    if let Some(name) = path.file_stem() {
                        if let Some(seq_str) = name.to_string_lossy().strip_prefix("sstable_") {
                            if let Ok(seq) = seq_str.parse::<u64>() {
                                let cur = self.sequence.load(Ordering::Relaxed);
                                if seq > cur { self.sequence.store(seq, Ordering::Relaxed); }
                            }
                        }
                    }
                    let ssts = unsafe { &mut *self.sstables.get() };
                    ssts.push((meta, Arc::new(data)));
                }
                Err(_) => continue,
            }
        }
        Ok(())
    }

    fn put(&self, key: &[u8], value: &[u8]) -> io::Result<()> {
        self.wal.lock().unwrap().append(key, value, WAL_PUT)?;
        let mt = unsafe { &mut *self.memtable.get() };
        let old_size = mt.get(key).map_or(0, |v| v.len());
        mt.insert(key.to_vec(), value.to_vec());
        self.memtable_size.fetch_add((key.len() + value.len()) as u64 - old_size as u64, Ordering::Relaxed);
        self.stats_writes.fetch_add(1, Ordering::Relaxed);
        Ok(())
    }

    fn get(&self, key: &[u8]) -> Option<Vec<u8>> {
        self.stats_reads.fetch_add(1, Ordering::Relaxed);

        // 1. Cache
        {
            let mut cache = self.cache.lock().unwrap();
            if let Some(val) = cache.get(key) {
                self.stats_cache_hits.fetch_add(1, Ordering::Relaxed);
                return if val.is_empty() { None } else { Some(val) };
            }
            self.stats_cache_misses.fetch_add(1, Ordering::Relaxed);
        }

        // 2. MemTable
        let mt = unsafe { &*self.memtable.get() };
        if let Some(val) = mt.get(key) {
            if val.is_empty() { return None; }
            self.cache.lock().unwrap().put(key, val);
            return Some(val.clone());
        }

        // 3. SSTables (newest first)
        let ssts = unsafe { &*self.sstables.get() };
        for (meta, data) in ssts.iter().rev() {
            if let Some(val) = meta.get(key, data) {
                if val.is_empty() { return None; }
                self.cache.lock().unwrap().put(key, &val);
                return Some(val);
            }
        }

        None
    }

    fn delete(&self, key: &[u8]) -> io::Result<()> {
        self.wal.lock().unwrap().append(key, &[], WAL_DELETE)?;
        let mt = unsafe { &mut *self.memtable.get() };
        mt.insert(key.to_vec(), Vec::new());
        self.cache.lock().unwrap().delete(key);
        Ok(())
    }

    fn flush(&self) -> io::Result<()> {
        let mt = unsafe { &*self.memtable.get() };
        if mt.is_empty() { return Ok(()); }

        let entries: Vec<(Vec<u8>, Vec<u8>)> = mt.iter()
            .filter(|(_, v)| !v.is_empty())
            .map(|(k, v)| (k.clone(), v.clone()))
            .collect();

        if entries.is_empty() { return Ok(()); }

        let seq = self.sequence.fetch_add(1, Ordering::Relaxed) + 1;
        let path = SSTableMeta::create(&entries, &self.data_dir, seq)?;

        let meta = SSTableMeta::open(&path)?;
        let file = File::open(&path)?;
        let mmap = unsafe { memmap2::Mmap::map(&file)? };
        let data: Vec<u8> = mmap.to_vec();

        let ssts = unsafe { &mut *self.sstables.get() };
        ssts.push((meta, Arc::new(data)));

        let mt_mut = unsafe { &mut *self.memtable.get() };
        mt_mut.clear();
        self.memtable_size.store(0, Ordering::Relaxed);

        self.wal.lock().unwrap().truncate()?;
        Ok(())
    }

    fn compact(&self) -> io::Result<()> {
        let ssts = unsafe { &*self.sstables.get() };
        if ssts.len() < 2 { return Ok(()); }

        let mut merged = BTreeMap::new();
        for (meta, data) in ssts.iter() {
            for (key, value) in meta.iter_all(data) {
                if !value.is_empty() {
                    merged.insert(key, value);
                }
            }
        }

        if merged.is_empty() { return Ok(()); }

        let entries: Vec<(Vec<u8>, Vec<u8>)> = merged.into_iter().collect();
        let seq = self.sequence.fetch_add(1, Ordering::Relaxed) + 1;
        let path = SSTableMeta::create(&entries, &self.data_dir, seq)?;

        let old_paths: Vec<PathBuf> = ssts.iter().map(|(m, _)| m.path.clone()).collect();
        for p in &old_paths { fs::remove_file(p).ok(); }

        let ssts_mut = unsafe { &mut *self.sstables.get() };
        ssts_mut.clear();

        let meta = SSTableMeta::open(&path)?;
        let file = File::open(&path)?;
        let mmap = unsafe { memmap2::Mmap::map(&file)? };
        let data: Vec<u8> = mmap.to_vec();
        ssts_mut.push((meta, Arc::new(data)));

        Ok(())
    }

    fn needs_flush(&self) -> bool {
        self.memtable_size.load(Ordering::Relaxed) >= self.memtable_size_limit
    }

    fn close(&self) -> io::Result<()> {
        self.flush()?;
        self.wal.lock().unwrap().close()?;
        Ok(())
    }

    fn memtable_len(&self) -> usize {
        let mt = unsafe { &*self.memtable.get() };
        mt.len()
    }
    fn memtable_bytes(&self) -> u64 { self.memtable_size.load(Ordering::Relaxed) }
    fn sstable_count(&self) -> usize {
        let ssts = unsafe { &*self.sstables.get() };
        ssts.len()
    }
}

// ============================================================
// C FFI Interface
// ============================================================

fn alloc_bytes(data: &[u8]) -> (*mut u8, usize) {
    let len = data.len();
    if len == 0 { return (std::ptr::null_mut(), 0); }
    let mut v = Vec::with_capacity(len);
    unsafe { std::ptr::copy_nonoverlapping(data.as_ptr(), v.as_mut_ptr(), len); }
    let ptr = v.as_mut_ptr();
    std::mem::forget(v);
    (ptr, len)
}

/// Create and open an LSM engine. Returns opaque handle.
#[no_mangle]
pub extern "C" fn ignis_engine_open(
    data_dir: *const u8, data_dir_len: usize,
    memtable_size_limit: u64,
    cache_size: usize,
) -> *mut LSMEngine {
    if data_dir.is_null() { return std::ptr::null_mut(); }
    let dir = unsafe { std::str::from_utf8_unchecked(std::slice::from_raw_parts(data_dir, data_dir_len)) };
    match LSMEngine::open(dir, memtable_size_limit, cache_size) {
        Ok(engine) => Box::into_raw(Box::new(engine)),
        Err(_) => std::ptr::null_mut(),
    }
}

#[no_mangle]
pub extern "C" fn ignis_engine_close(ptr: *mut LSMEngine) -> i32 {
    if ptr.is_null() { return -1; }
    let engine = unsafe { Box::from_raw(ptr) };
    match engine.close() { Ok(_) => 0, Err(_) => -1 }
}

#[no_mangle]
pub extern "C" fn ignis_engine_put(
    ptr: *mut LSMEngine,
    key: *const u8, key_len: usize,
    value: *const u8, value_len: usize,
) -> i32 {
    if ptr.is_null() || key.is_null() || value.is_null() { return -1; }
    let engine = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    let v = unsafe { std::slice::from_raw_parts(value, value_len) };
    match engine.put(k, v) {
        Ok(_) => {
            if engine.needs_flush() { engine.flush().ok(); }
            0
        }
        Err(_) => -1,
    }
}

/// Get value. Returns 1 if found, 0 if not. Caller frees out_value with ignis_free_bytes.
#[no_mangle]
pub extern "C" fn ignis_engine_get(
    ptr: *mut LSMEngine,
    key: *const u8, key_len: usize,
    out_value: *mut *mut u8, out_len: *mut usize,
) -> i32 {
    if ptr.is_null() || key.is_null() || out_value.is_null() || out_len.is_null() { return 0; }
    let engine = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    match engine.get(k) {
        Some(val) => {
            let (ptr, len) = alloc_bytes(&val);
            unsafe { *out_value = ptr; *out_len = len; }
            1
        }
        None => 0,
    }
}

#[no_mangle]
pub extern "C" fn ignis_engine_delete(
    ptr: *mut LSMEngine,
    key: *const u8, key_len: usize,
) -> i32 {
    if ptr.is_null() || key.is_null() { return -1; }
    let engine = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    match engine.delete(k) { Ok(_) => 0, Err(_) => -1 }
}

#[no_mangle]
pub extern "C" fn ignis_engine_flush(ptr: *mut LSMEngine) -> i32 {
    if ptr.is_null() { return -1; }
    let engine = unsafe { &*ptr };
    match engine.flush() { Ok(_) => 0, Err(_) => -1 }
}

#[no_mangle]
pub extern "C" fn ignis_engine_compact(ptr: *mut LSMEngine) -> i32 {
    if ptr.is_null() { return -1; }
    let engine = unsafe { &*ptr };
    match engine.compact() { Ok(_) => 0, Err(_) => -1 }
}

#[no_mangle]
pub extern "C" fn ignis_engine_memtable_len(ptr: *mut LSMEngine) -> usize {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.memtable_len()
}

#[no_mangle]
pub extern "C" fn ignis_engine_memtable_bytes(ptr: *mut LSMEngine) -> u64 {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.memtable_bytes()
}

#[no_mangle]
pub extern "C" fn ignis_engine_sstable_count(ptr: *mut LSMEngine) -> usize {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.sstable_count()
}

#[no_mangle]
pub extern "C" fn ignis_engine_stats_writes(ptr: *mut LSMEngine) -> u64 {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.stats_writes.load(Ordering::Relaxed)
}

#[no_mangle]
pub extern "C" fn ignis_engine_stats_reads(ptr: *mut LSMEngine) -> u64 {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.stats_reads.load(Ordering::Relaxed)
}

#[no_mangle]
pub extern "C" fn ignis_engine_stats_cache_hits(ptr: *mut LSMEngine) -> u64 {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.stats_cache_hits.load(Ordering::Relaxed)
}

#[no_mangle]
pub extern "C" fn ignis_engine_stats_cache_misses(ptr: *mut LSMEngine) -> u64 {
    if ptr.is_null() { return 0; }
    unsafe { &*ptr }.stats_cache_misses.load(Ordering::Relaxed)
}

#[no_mangle]
pub extern "C" fn ignis_free_bytes(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        unsafe { let _ = Vec::from_raw_parts(ptr, len, len); }
    }
}
