package benchmarks

import (
	"os"
	"testing"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
)

// appendInt appends decimal digits of n to buf without allocation.
func appendInt(buf []byte, n int) []byte {
	if n == 0 {
		return append(buf, '0')
	}
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	// Write digits in reverse, then reverse them
	start := len(buf)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// Reverse the digits we just appended
	for i, j := start, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}

func makeKey(buf []byte, i int) []byte {
	buf = append(buf[:0], "key:"...)
	return appendInt(buf, i)
}

func makeValue(buf []byte, i int) []byte {
	buf = append(buf[:0], "value:"...)
	return appendInt(buf, i)
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

	// Warmup: fill sync.Pool caches so benchmark measures steady-state
	for i := 0; i < 10000; i++ {
		key := makeKey(keyBuf, i)
		value := makeValue(valBuf, i)
		eng.Put(key, value)
	}

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
