package benchmarks

import (
	"os"
	"strconv"
	"testing"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
)

// Pre-allocated key/value buffers to avoid fmt.Sprintf overhead in benchmarks
func makeKey(buf []byte, i int) []byte {
	copy(buf, "key:")
	return append(buf[:4], strconv.Itoa(i)...)
}

func makeValue(buf []byte, i int) []byte {
	copy(buf, "value:")
	return append(buf[:6], strconv.Itoa(i)...)
}

func BenchmarkWrite(b *testing.B) {
	dir, _ := os.MkdirTemp("", "ignisdb-bench-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	keyBuf := make([]byte, 0, 32)
	valBuf := make([]byte, 0, 32)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := makeKey(keyBuf, i)
		value := makeValue(valBuf, i)
		eng.Put(key, value)
	}
}

func BenchmarkReadHot(b *testing.B) {
	dir, _ := os.MkdirTemp("", "ignisdb-bench-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	// Pre-populate
	keyBuf := make([]byte, 0, 32)
	for i := 0; i < 10000; i++ {
		eng.Put(makeKey(keyBuf, i), []byte("value"))
	}

	keys := make([][]byte, 10000)
	for i := 0; i < 10000; i++ {
		keys[i] = makeKey(make([]byte, 0, 32), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng.Get(keys[i%10000])
	}
}

func BenchmarkReadCold(b *testing.B) {
	dir, _ := os.MkdirTemp("", "ignisdb-bench-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	// Pre-populate and flush to SSTables
	keyBuf := make([]byte, 0, 32)
	for i := 0; i < 100000; i++ {
		eng.Put(makeKey(keyBuf, i), []byte("value"))
	}
	eng.Flush()

	keys := make([][]byte, 100000)
	for i := 0; i < 100000; i++ {
		keys[i] = makeKey(make([]byte, 0, 32), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng.Get(keys[i%100000])
	}
}

func BenchmarkMixedWorkload(b *testing.B) {
	dir, _ := os.MkdirTemp("", "ignisdb-bench-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	// Pre-populate
	keyBuf := make([]byte, 0, 32)
	for i := 0; i < 10000; i++ {
		eng.Put(makeKey(keyBuf, i), []byte("value"))
	}

	keys := make([][]byte, 10000)
	for i := 0; i < 10000; i++ {
		keys[i] = makeKey(make([]byte, 0, 32), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%10 == 0 {
			eng.Put(keys[i%10000], []byte("updated"))
		} else {
			eng.Get(keys[i%10000])
		}
	}
}
