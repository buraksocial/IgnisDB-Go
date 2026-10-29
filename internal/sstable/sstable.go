// Package sstable implements Sorted String Tables with mmap access.
//
// Format:
// [Header: 16 bytes]
//   - magic: 4 bytes ("IGDB")
//   - version: 2 bytes
//   - entry_count: 4 bytes
//   - reserved: 6 bytes
//
// [Data Block: variable]
//   - key_len (4 bytes) + key + value_len (4 bytes) + value
//   - ... (sorted by key)
//
// [Index Block: variable]
//   - sparse index: key_len(4) + key + offset(8) for each ~4KB block
//
// [Bloom Filter: variable]
//   - serialized BloomFilter
//
// [Footer: 16 bytes]
//   - index_offset: 8 bytes
//   - bloom_offset: 8 bytes
package sstable

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"syscall"

	"github.com/buraksocial/IgnisDB-Go/internal/bloom"
	"github.com/buraksocial/IgnisDB-Go/internal/memtable"
)

const (
	// Magic is the SSTable file magic number.
	Magic = "IGDB"
	// Version is the SSTable format version.
	Version = 1
	// BlockSize is the sparse index granularity.
	BlockSize = 4096
	// HeaderSize is the size of the SSTable header.
	HeaderSize = 16
	// FooterSize is the size of the SSTable footer.
	FooterSize = 16
)

// IndexEntry represents a sparse index entry.
type IndexEntry struct {
	Key    []byte
	Offset int64
}

// SSTable is an immutable sorted string table stored on disk.
type SSTable struct {
	Path        string
	file        *os.File
	data        []byte // mmap'd region
	entryCount  int
	indexOffset int64
	bloomOffset int64
	bloomFilter *bloom.Filter
	index       []IndexEntry
	loaded      bool
}

// Create creates an SSTable from a MemTable.
func Create(mt *memtable.SkipList, dataDir string, sequence int64) (*SSTable, error) {
	items := mt.Items()
	if len(items) == 0 {
		return nil, fmt.Errorf("cannot create SSTable from empty MemTable")
	}

	path := fmt.Sprintf("%s/sstable_%016d.db", dataDir, sequence)

	// Build bloom filter
	keys := make([][]byte, len(items))
	for i, item := range items {
		keys[i] = item.Key
	}
	bf := bloom.NewFromKeys(keys, 0.01)

	// Write SSTable file
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create SSTable file: %w", err)
	}

	// === Header (16 bytes) ===
	header := make([]byte, HeaderSize)
	copy(header[0:4], Magic)
	binary.LittleEndian.PutUint16(header[4:6], Version)
	binary.LittleEndian.PutUint32(header[6:10], uint32(len(items)))
	// bytes 10-15 reserved (zeros)
	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}

	// === Data Block ===
	var indexEntries []IndexEntry
	bytesSinceLastIndex := 0

	for i, item := range items {
		currentOffset, _ := f.Seek(0, os.SEEK_CUR)

		// Record index entry at block boundaries
		if bytesSinceLastIndex >= BlockSize || i == 0 {
			indexEntries = append(indexEntries, IndexEntry{
				Key:    item.Key,
				Offset: currentOffset,
			})
			bytesSinceLastIndex = 0
		}

		// Write entry: key_len(4) + key + value_len(4) + value
		keyLenBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(keyLenBuf, uint32(len(item.Key)))
		f.Write(keyLenBuf)
		f.Write(item.Key)

		valueLenBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(valueLenBuf, uint32(len(item.Value)))
		f.Write(valueLenBuf)
		f.Write(item.Value)

		bytesSinceLastIndex += 4 + len(item.Key) + 4 + len(item.Value)
	}

	// === Index Block ===
	indexOffset, _ := f.Seek(0, os.SEEK_CUR)
	for _, ie := range indexEntries {
		keyLenBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(keyLenBuf, uint32(len(ie.Key)))
		f.Write(keyLenBuf)
		f.Write(ie.Key)

		offsetBuf := make([]byte, 8)
		binary.LittleEndian.PutUint64(offsetBuf, uint64(ie.Offset))
		f.Write(offsetBuf)
	}

	// === Bloom Filter ===
	bloomOffset, _ := f.Seek(0, os.SEEK_CUR)
	bloomData := bf.Serialize()
	f.Write(bloomData)

	// === Footer (16 bytes) ===
	footer := make([]byte, FooterSize)
	binary.LittleEndian.PutUint64(footer[0:8], uint64(indexOffset))
	binary.LittleEndian.PutUint64(footer[8:16], uint64(bloomOffset))
	f.Write(footer)

	f.Sync()
	f.Close()

	// Open with mmap
	return Open(path)
}

// Open opens an existing SSTable file with mmap.
func Open(path string) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open SSTable: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	if info.Size() == 0 {
		f.Close()
		return nil, fmt.Errorf("empty SSTable file: %s", path)
	}

	// Native mmap — zero-copy reads
	data, err := syscall.Mmap(int(f.Fd()), 0, int(info.Size()),
		syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap SSTable: %w", err)
	}

	sst := &SSTable{
		Path: path,
		file: f,
		data: data,
	}

	// Read footer (last 16 bytes)
	footerStart := len(data) - FooterSize
	sst.indexOffset = int64(binary.LittleEndian.Uint64(data[footerStart : footerStart+8]))
	sst.bloomOffset = int64(binary.LittleEndian.Uint64(data[footerStart+8 : footerStart+16]))

	// Read header
	magic := string(data[0:4])
	if magic != Magic {
		syscall.Munmap(data)
		f.Close()
		return nil, fmt.Errorf("invalid SSTable magic: %s", magic)
	}

	// version := binary.LittleEndian.Uint16(data[4:6])
	sst.entryCount = int(binary.LittleEndian.Uint32(data[6:10]))

	// Load bloom filter
	if sst.bloomOffset > 0 && sst.bloomOffset < int64(len(data)) {
		bloomEnd := int64(len(data) - FooterSize)
		if sst.bloomOffset < bloomEnd {
			sst.bloomFilter = bloom.Deserialize(data[sst.bloomOffset:bloomEnd])
		}
	}

	// Load sparse index
	if sst.indexOffset > 0 && sst.indexOffset < sst.bloomOffset {
		sst.parseIndex(data[sst.indexOffset:sst.bloomOffset])
	}

	sst.loaded = true
	return sst, nil
}

// parseIndex parses the sparse index from raw bytes.
func (sst *SSTable) parseIndex(data []byte) {
	sst.index = nil
	pos := 0
	for pos < len(data) {
		if pos+4 > len(data) {
			break
		}
		keyLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4

		if pos+keyLen+8 > len(data) {
			break
		}
		key := make([]byte, keyLen)
		copy(key, data[pos:pos+keyLen])
		pos += keyLen

		offset := int64(binary.LittleEndian.Uint64(data[pos : pos+8]))
		pos += 8

		sst.index = append(sst.index, IndexEntry{Key: key, Offset: offset})
	}
}

// MightContain checks the bloom filter for possible key presence.
func (sst *SSTable) MightContain(key []byte) bool {
	if sst.bloomFilter == nil {
		return true
	}
	return sst.bloomFilter.MightContain(key)
}

// Get looks up a key in the SSTable.
// Uses bloom filter → sparse index → linear scan within block.
func (sst *SSTable) Get(key []byte) ([]byte, bool) {
	// Bloom filter check
	if !sst.MightContain(key) {
		return nil, false
	}

	// Find approximate position using sparse index
	startOffset := sst.findBlockOffset(key)

	// Linear scan within block
	offset := startOffset
	for offset < sst.indexOffset {
		if offset+4 > int64(len(sst.data)) {
			break
		}

		keyLen := int64(binary.LittleEndian.Uint32(sst.data[offset : offset+4]))
		if keyLen == 0 || keyLen > 10*1024*1024 {
			break
		}
		offset += 4

		if offset+keyLen > int64(len(sst.data)) {
			break
		}
		currentKey := sst.data[offset : offset+keyLen]
		offset += keyLen

		if offset+4 > int64(len(sst.data)) {
			break
		}
		valueLen := int64(binary.LittleEndian.Uint32(sst.data[offset : offset+4]))
		offset += 4

		cmp := bytes.Compare(currentKey, key)
		if cmp == 0 {
			if offset+valueLen > int64(len(sst.data)) {
				break
			}
			value := make([]byte, valueLen)
			copy(value, sst.data[offset:offset+valueLen])
			return value, true
		} else if cmp > 0 {
			break // Past our key (data is sorted)
		}

		offset += valueLen
	}

	return nil, false
}

// findBlockOffset finds the starting offset for the block that might contain key.
func (sst *SSTable) findBlockOffset(key []byte) int64 {
	if len(sst.index) == 0 {
		return HeaderSize
	}

	// Binary search in sparse index
	idx := sort.Search(len(sst.index), func(i int) bool {
		return bytes.Compare(sst.index[i].Key, key) > 0
	})

	if idx == 0 {
		return HeaderSize
	}
	return sst.index[idx-1].Offset
}

// IterItem represents a key-value pair from iteration.
type IterItem struct {
	Key   []byte
	Value []byte
}

// IterAll iterates over all key-value pairs in sorted order.
func (sst *SSTable) IterAll() []IterItem {
	items := make([]IterItem, 0, sst.entryCount)
	offset := int64(HeaderSize)

	for i := 0; i < sst.entryCount; i++ {
		if offset+4 > int64(len(sst.data)) {
			break
		}
		keyLen := int64(binary.LittleEndian.Uint32(sst.data[offset : offset+4]))
		offset += 4

		if offset+keyLen > int64(len(sst.data)) {
			break
		}
		key := make([]byte, keyLen)
		copy(key, sst.data[offset:offset+keyLen])
		offset += keyLen

		if offset+4 > int64(len(sst.data)) {
			break
		}
		valueLen := int64(binary.LittleEndian.Uint32(sst.data[offset : offset+4]))
		offset += 4

		if offset+valueLen > int64(len(sst.data)) {
			break
		}
		value := make([]byte, valueLen)
		copy(value, sst.data[offset:offset+valueLen])
		offset += valueLen

		items = append(items, IterItem{Key: key, Value: value})
	}

	return items
}

// EntryCount returns the number of entries.
func (sst *SSTable) EntryCount() int {
	return sst.entryCount
}

// FileSize returns the file size in bytes.
func (sst *SSTable) FileSize() int64 {
	info, err := os.Stat(sst.Path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// Close closes the SSTable, unmapping memory and closing the file.
func (sst *SSTable) Close() error {
	if sst.data != nil {
		syscall.Munmap(sst.data)
		sst.data = nil
	}
	if sst.file != nil {
		err := sst.file.Close()
		sst.file = nil
		return err
	}
	sst.loaded = false
	return nil
}

// Merge merges multiple SSTables into one, deduplicating by key (latest wins).
func Merge(sstables []*SSTable, dataDir string, sequence int64) (*SSTable, error) {
	// Collect all entries, newest SSTable first
	merged := memtable.New()

	// Process from oldest to newest so newest values overwrite
	for i := len(sstables) - 1; i >= 0; i-- {
		items := sstables[i].IterAll()
		for _, item := range items {
			// Skip tombstones during compaction
			if len(item.Value) > 0 {
				merged.Put(item.Key, item.Value)
			}
		}
	}

	if merged.Len() == 0 {
		return nil, fmt.Errorf("merge resulted in empty SSTable")
	}

	return Create(merged, dataDir, sequence)
}
