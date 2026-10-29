// Package wal implements a Write-Ahead Log for crash recovery.
// All writes are appended to WAL before being applied to MemTable.
//
// Optimized with buffered writes and batch fsync for reduced I/O overhead.
package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const (
	// WalPut indicates a PUT operation.
	WalPut uint8 = 1
	// WalDelete indicates a DELETE operation.
	WalDelete uint8 = 2
	// writeBufferSize is the WAL write buffer size (256 KB).
	writeBufferSize = 256 * 1024
	// smallEntryThreshold is the max size for pooled WAL buffers.
	smallEntryThreshold = 512
)

// walBufPool reuses WAL entry buffers to reduce allocations.
var walBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 0, smallEntryThreshold)
		return &buf
	},
}

// Entry represents a single WAL entry.
type Entry struct {
	Type  uint8
	Key   []byte
	Value []byte
}

// WAL is a Write-Ahead Log for crash safety.
//
// Format per entry:
// [type: 1 byte][key_len: 4 bytes][key][value_len: 4 bytes][value][checksum: 4 bytes]
type WAL struct {
	dataDir  string
	walPath  string
	file     *os.File
	writer   *bufio.Writer
	position int64
	mu       sync.Mutex
}

// Open opens or creates a WAL in the given data directory.
func Open(dataDir string) *WAL {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("WAL: failed to create data dir: %v", err)
	}

	w := &WAL{
		dataDir: dataDir,
		walPath: filepath.Join(dataDir, "wal.log"),
	}
	return w
}

// OpenFile opens the WAL file for appending with buffered writer.
func (w *WAL) OpenFile() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		return nil
	}

	f, err := os.OpenFile(w.walPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open WAL: %w", err)
	}
	pos, _ := f.Seek(0, io.SeekEnd)
	w.file = f
	w.writer = bufio.NewWriterSize(f, writeBufferSize)
	w.position = pos
	return nil
}

// Close closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		if w.writer != nil {
			w.writer.Flush()
		}
		w.file.Sync()
		err := w.file.Close()
		w.file = nil
		w.writer = nil
		return err
	}
	return nil
}

// Append appends an entry to the WAL (buffered).
func (w *WAL) Append(key, value []byte, entryType uint8) error {
	if w.file == nil {
		if err := w.OpenFile(); err != nil {
			return err
		}
	}

	// Build entry data using pre-allocated buffer
	keyLen := len(key)
	valueLen := len(value)
	entrySize := 1 + 4 + keyLen + 4 + valueLen + 4 // type + keyLen + key + valueLen + value + checksum

	// Use pooled buffer for small entries, heap for large
	var buf []byte
	var pooled *[]byte
	if entrySize <= smallEntryThreshold {
		pooled = walBufPool.Get().(*[]byte)
		buf = (*pooled)[:entrySize]
	} else {
		buf = make([]byte, entrySize)
	}

	buf[0] = entryType
	binary.LittleEndian.PutUint32(buf[1:5], uint32(keyLen))
	copy(buf[5:5+keyLen], key)
	binary.LittleEndian.PutUint32(buf[5+keyLen:9+keyLen], uint32(valueLen))
	if valueLen > 0 {
		copy(buf[9+keyLen:9+keyLen+valueLen], value)
	}

	// Calculate checksum over entry data (excluding checksum field)
	entryData := buf[:entrySize-4]
	checksum := crc32.ChecksumIEEE(entryData)
	binary.LittleEndian.PutUint32(buf[entrySize-4:], checksum)

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer == nil {
		f, err := os.OpenFile(w.walPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		w.file = f
		w.writer = bufio.NewWriterSize(f, writeBufferSize)
	}

	_, err := w.writer.Write(buf)
	// Return pooled buffer immediately after copy into bufio.Writer
	if pooled != nil {
		*pooled = (*pooled)[:0]
		walBufPool.Put(pooled)
	}
	if err != nil {
		return err
	}
	// Note: We don't flush/fsync on every write for performance.
	// Flush happens periodically or on Truncate.
	return nil
}

// Sync forces the WAL buffer to disk.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			return err
		}
	}
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// Replay replays all WAL entries.
func (w *WAL) Replay() ([]Entry, error) {
	if _, err := os.Stat(w.walPath); os.IsNotExist(err) {
		return nil, nil
	}

	f, err := os.Open(w.walPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, writeBufferSize)
	var entries []Entry

	for {
		// Read type
		typeByte, err := reader.ReadByte()
		if err != nil {
			break
		}
		entryType := typeByte

		// Read key length
		keyLenBuf := make([]byte, 4)
		if _, err := io.ReadFull(reader, keyLenBuf); err != nil {
			break
		}
		keyLen := binary.LittleEndian.Uint32(keyLenBuf)

		// Sanity check
		if keyLen > 10*1024*1024 {
			log.Printf("WAL corruption: invalid key_len %d", keyLen)
			break
		}

		// Read key
		key := make([]byte, keyLen)
		if _, err := io.ReadFull(reader, key); err != nil {
			break
		}

		// Read value length
		valueLenBuf := make([]byte, 4)
		if _, err := io.ReadFull(reader, valueLenBuf); err != nil {
			break
		}
		valueLen := binary.LittleEndian.Uint32(valueLenBuf)

		// Read value
		var value []byte
		if valueLen > 0 {
			value = make([]byte, valueLen)
			if _, err := io.ReadFull(reader, value); err != nil {
				break
			}
		}

		// Read checksum
		checksumBuf := make([]byte, 4)
		if _, err := io.ReadFull(reader, checksumBuf); err != nil {
			break
		}
		storedChecksum := binary.LittleEndian.Uint32(checksumBuf)

		// Verify checksum
		entryData := make([]byte, 1+4+keyLen+4+valueLen)
		entryData[0] = entryType
		binary.LittleEndian.PutUint32(entryData[1:5], keyLen)
		copy(entryData[5:5+keyLen], key)
		binary.LittleEndian.PutUint32(entryData[5+keyLen:9+keyLen], valueLen)
		if valueLen > 0 {
			copy(entryData[9+keyLen:], value)
		}
		computedChecksum := crc32.ChecksumIEEE(entryData)

		if storedChecksum != computedChecksum {
			log.Printf("WAL checksum mismatch, stopping replay")
			break
		}

		entries = append(entries, Entry{
			Type:  entryType,
			Key:   key,
			Value: value,
		})
	}

	return entries, nil
}

// Truncate truncates the WAL after successful flush.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		w.writer.Flush()
	}
	if w.file != nil {
		w.file.Sync()
		w.file.Close()
		w.file = nil
		w.writer = nil
	}

	// Atomic truncate: rename to .old, create new, delete old
	oldPath := w.walPath + ".old"

	if _, err := os.Stat(w.walPath); err == nil {
		os.Rename(w.walPath, oldPath)
	}

	// Create new empty WAL
	f, err := os.Create(w.walPath)
	if err != nil {
		return err
	}
	w.file = f
	w.writer = bufio.NewWriterSize(f, writeBufferSize)
	w.position = 0

	// Delete old WAL
	os.Remove(oldPath)

	return nil
}

// FileSize returns the current WAL file size.
func (w *WAL) FileSize() int64 {
	info, err := os.Stat(w.walPath)
	if err != nil {
		return 0
	}
	return info.Size()
}
