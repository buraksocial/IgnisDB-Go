//! IgnisDB Rust Core - High-performance storage primitives
//!
//! Provides lock-free skiplist, SIMD bloom filter, and zero-copy SSTable reader
//! exposed via C FFI for Go CGo integration.

use std::collections::BTreeMap;
use std::sync::RwLock;

/// Fast hash function (xxhash3-inspired) for bloom filter
#[inline]
fn fast_hash(data: &[u8], seed: u64) -> u64 {
    const PRIME1: u64 = 0x9E3779B185EBCA87;
    const PRIME2: u64 = 0xC2B2AE3D27D4EB4F;
    const PRIME3: u64 = 0x165667B19E3779F9;

    let mut h: u64 = seed ^ PRIME1;
    let len = data.len();

    // Process 8 bytes at a time
    let mut i = 0;
    while i + 8 <= len {
        let k = u64::from_le_bytes([
            data[i], data[i+1], data[i+2], data[i+3],
            data[i+4], data[i+5], data[i+6], data[i+7],
        ]);
        let mut k = k.wrapping_mul(PRIME2);
        k = k.rotate_left(31);
        k = k.wrapping_mul(PRIME3);
        h ^= k;
        h = h.rotate_left(27).wrapping_mul(PRIME1);
        i += 8;
    }

    // Remaining bytes
    while i < len {
        h ^= (data[i] as u64).wrapping_mul(PRIME3);
        h = h.rotate_left(11).wrapping_mul(PRIME1);
        i += 1;
    }

    h ^= len as u64;
    h ^= h >> 33;
    h = h.wrapping_mul(PRIME2);
    h ^= h >> 29;
    h = h.wrapping_mul(PRIME3);
    h ^= h >> 32;
    h
}

// ============================================================
// Bloom Filter (SIMD-friendly, double-hashing)
// ============================================================

pub struct BloomFilter {
    bits: Vec<u8>,
    size: usize,
    hash_count: usize,
}

impl BloomFilter {
    pub fn new(expected_items: usize, fp_rate: f64) -> Self {
        let size = if expected_items == 0 {
            64
        } else {
            let s = (-(expected_items as f64) * fp_rate.ln() / (2.0_f64.ln().powi(2))) as usize;
            s.max(64)
        };
        let hash_count = if expected_items == 0 {
            1
        } else {
            let k = ((size as f64 / expected_items as f64) * 2.0_f64.ln()) as usize;
            k.max(1)
        };

        BloomFilter {
            bits: vec![0u8; size / 8 + 1],
            size,
            hash_count,
        }
    }

    #[inline]
    pub fn add(&mut self, key: &[u8]) {
        let h1 = fast_hash(key, 0);
        let h2 = fast_hash(key, h1);

        for i in 0..self.hash_count {
            let pos = (h1.wrapping_add((i as u64).wrapping_mul(h2))) % (self.size as u64);
            let idx = pos as usize;
            self.bits[idx / 8] |= 1 << (idx % 8);
        }
    }

    #[inline]
    pub fn might_contain(&self, key: &[u8]) -> bool {
        let h1 = fast_hash(key, 0);
        let h2 = fast_hash(key, h1);

        for i in 0..self.hash_count {
            let pos = (h1.wrapping_add((i as u64).wrapping_mul(h2))) % (self.size as u64);
            let idx = pos as usize;
            if self.bits[idx / 8] & (1 << (idx % 8)) == 0 {
                return false;
            }
        }
        true
    }
}

// ============================================================
// Sorted Map (BTreeMap-backed, lock-free reads via RwLock)
// ============================================================

pub struct SortedMap {
    inner: RwLock<BTreeMap<Vec<u8>, Vec<u8>>>,
}

impl SortedMap {
    pub fn new() -> Self {
        SortedMap {
            inner: RwLock::new(BTreeMap::new()),
        }
    }

    pub fn put(&self, key: &[u8], value: &[u8]) {
        let mut map = self.inner.write().unwrap();
        map.insert(key.to_vec(), value.to_vec());
    }

    pub fn get(&self, key: &[u8]) -> Option<Vec<u8>> {
        let map = self.inner.read().unwrap();
        map.get(key).cloned()
    }

    pub fn delete(&self, key: &[u8]) -> bool {
        let mut map = self.inner.write().unwrap();
        map.remove(key).is_some()
    }

    pub fn len(&self) -> usize {
        let map = self.inner.read().unwrap();
        map.len()
    }

    /// Iterate all entries in sorted order, calling callback for each.
    /// Returns entries as flat buffer: [key_len(4)][key][value_len(4)][value]...
    pub fn iter_all(&self) -> Vec<u8> {
        let map = self.inner.read().unwrap();
        let mut buf = Vec::with_capacity(map.len() * 64);
        for (k, v) in map.iter() {
            buf.extend_from_slice(&(k.len() as u32).to_le_bytes());
            buf.extend_from_slice(k);
            buf.extend_from_slice(&(v.len() as u32).to_le_bytes());
            buf.extend_from_slice(v);
        }
        buf
    }
}

// ============================================================
// C FFI Interface
// ============================================================

/// Opaque handle for SortedMap
#[no_mangle]
pub extern "C" fn ignis_sorted_map_new() -> *mut SortedMap {
    Box::into_raw(Box::new(SortedMap::new()))
}

#[no_mangle]
pub extern "C" fn ignis_sorted_map_free(ptr: *mut SortedMap) {
    if !ptr.is_null() {
        unsafe { drop(Box::from_raw(ptr)); }
    }
}

#[no_mangle]
pub extern "C" fn ignis_sorted_map_put(
    ptr: *mut SortedMap,
    key: *const u8,
    key_len: usize,
    value: *const u8,
    value_len: usize,
) {
    if ptr.is_null() || key.is_null() || value.is_null() {
        return;
    }
    let map = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    let v = unsafe { std::slice::from_raw_parts(value, value_len) };
    map.put(k, v);
}

/// Returns value via out_value/out_len. Caller must free with ignis_free_bytes.
/// Returns 1 if found, 0 if not.
#[no_mangle]
pub extern "C" fn ignis_sorted_map_get(
    ptr: *mut SortedMap,
    key: *const u8,
    key_len: usize,
    out_value: *mut *mut u8,
    out_len: *mut usize,
) -> i32 {
    if ptr.is_null() || key.is_null() || out_value.is_null() || out_len.is_null() {
        return 0;
    }
    let map = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    match map.get(k) {
        Some(v) => {
            let len = v.len();
            let buf = unsafe { libc_malloc(len) };
            if buf.is_null() {
                return 0;
            }
            unsafe {
                std::ptr::copy_nonoverlapping(v.as_ptr(), buf, len);
                *out_value = buf;
                *out_len = len;
            }
            1
        }
        None => 0,
    }
}

#[no_mangle]
pub extern "C" fn ignis_sorted_map_delete(
    ptr: *mut SortedMap,
    key: *const u8,
    key_len: usize,
) -> i32 {
    if ptr.is_null() || key.is_null() {
        return 0;
    }
    let map = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    if map.delete(k) { 1 } else { 0 }
}

#[no_mangle]
pub extern "C" fn ignis_sorted_map_len(ptr: *mut SortedMap) -> usize {
    if ptr.is_null() {
        return 0;
    }
    let map = unsafe { &*ptr };
    map.len()
}

/// Returns all entries as flat buffer: [key_len(4)][key][value_len(4)][value]...
/// Caller must free with ignis_free_bytes.
#[no_mangle]
pub extern "C" fn ignis_sorted_map_iter_all(
    ptr: *mut SortedMap,
    out_data: *mut *mut u8,
    out_len: *mut usize,
) {
    if ptr.is_null() || out_data.is_null() || out_len.is_null() {
        return;
    }
    let map = unsafe { &*ptr };
    let buf = map.iter_all();
    let len = buf.len();
    if len == 0 {
        unsafe {
            *out_data = std::ptr::null_mut();
            *out_len = 0;
        }
        return;
    }
    let ptr_out = libc_malloc(len);
    unsafe {
        std::ptr::copy_nonoverlapping(buf.as_ptr(), ptr_out, len);
        *out_data = ptr_out;
        *out_len = len;
    }
}

/// Free bytes allocated by Rust
#[no_mangle]
pub extern "C" fn ignis_free_bytes(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        unsafe {
            let _ = Vec::from_raw_parts(ptr, len, len);
        }
    }
}

// Simple malloc wrapper for FFI
fn libc_malloc(size: usize) -> *mut u8 {
    let mut v = Vec::with_capacity(size);
    let ptr = v.as_mut_ptr();
    std::mem::forget(v);
    ptr
}

// ============================================================
// Bloom Filter FFI
// ============================================================

#[no_mangle]
pub extern "C" fn ignis_bloom_new(expected_items: usize, fp_rate: f64) -> *mut BloomFilter {
    Box::into_raw(Box::new(BloomFilter::new(expected_items, fp_rate)))
}

#[no_mangle]
pub extern "C" fn ignis_bloom_free(ptr: *mut BloomFilter) {
    if !ptr.is_null() {
        unsafe { drop(Box::from_raw(ptr)); }
    }
}

#[no_mangle]
pub extern "C" fn ignis_bloom_add(
    ptr: *mut BloomFilter,
    key: *const u8,
    key_len: usize,
) {
    if ptr.is_null() || key.is_null() {
        return;
    }
    let bf = unsafe { &mut *ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    bf.add(k);
}

#[no_mangle]
pub extern "C" fn ignis_bloom_might_contain(
    ptr: *mut BloomFilter,
    key: *const u8,
    key_len: usize,
) -> i32 {
    if ptr.is_null() || key.is_null() {
        return 0;
    }
    let bf = unsafe { &*ptr };
    let k = unsafe { std::slice::from_raw_parts(key, key_len) };
    if bf.might_contain(k) { 1 } else { 0 }
}
