package benchmarks

import (
	"fmt"
	"os"
	"testing"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func BenchmarkWrite(b *testing.B) {
	dir, _ := os.MkdirTemp("", "ignisdb-bench-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key:%d", i))
		value := []byte(fmt.Sprintf("value:%d", i))
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
	for i := 0; i < 10000; i++ {
		eng.Put([]byte(fmt.Sprintf("key:%d", i)), []byte("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key:%d", i%10000))
		eng.Get(key)
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
	for i := 0; i < 100000; i++ {
		eng.Put([]byte(fmt.Sprintf("key:%d", i)), []byte("value"))
	}
	eng.Flush()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key:%d", i%100000))
		eng.Get(key)
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
	for i := 0; i < 10000; i++ {
		eng.Put([]byte(fmt.Sprintf("key:%d", i)), []byte("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%10 == 0 {
			// 10% writes
			key := []byte(fmt.Sprintf("key:%d", i))
			eng.Put(key, []byte("updated"))
		} else {
			// 90% reads
			key := []byte(fmt.Sprintf("key:%d", i%10000))
			eng.Get(key)
		}
	}
}
