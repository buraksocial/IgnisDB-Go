package tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func TestEngineBasicPutGet(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Put and Get
	eng.Put([]byte("key1"), []byte("value1"))
	val, ok := eng.Get([]byte("key1"))
	if !ok || string(val) != "value1" {
		t.Fatalf("expected value1, got %s (ok=%v)", val, ok)
	}

	// Non-existent key
	_, ok = eng.Get([]byte("nonexistent"))
	if ok {
		t.Fatal("expected not found")
	}
}

func TestEngineDelete(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	eng.Put([]byte("key1"), []byte("value1"))
	eng.Delete([]byte("key1"))

	_, ok := eng.Get([]byte("key1"))
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestEngineOverwrite(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	eng.Put([]byte("key1"), []byte("v1"))
	eng.Put([]byte("key1"), []byte("v2"))

	val, ok := eng.Get([]byte("key1"))
	if !ok || string(val) != "v2" {
		t.Fatalf("expected v2, got %s", val)
	}
}

func TestEngineFlushAndRecover(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	// Write data and flush
	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		eng.Put([]byte(fmt.Sprintf("key:%d", i)), []byte(fmt.Sprintf("value:%d", i)))
	}
	eng.Flush()
	eng.Close()

	// Reopen and verify
	eng2, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()

	for i := 0; i < 100; i++ {
		val, ok := eng2.Get([]byte(fmt.Sprintf("key:%d", i)))
		if !ok || string(val) != fmt.Sprintf("value:%d", i) {
			t.Fatalf("key:%d expected value:%d, got %s (ok=%v)", i, i, val, ok)
		}
	}
}

func TestEngineSortedSets(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	key := []byte("myzset")
	eng.ZAdd(key, 3.0, []byte("charlie"))
	eng.ZAdd(key, 1.0, []byte("alice"))
	eng.ZAdd(key, 2.0, []byte("bob"))

	// ZCard
	if card := eng.ZCard(key); card != 3 {
		t.Fatalf("expected 3 members, got %d", card)
	}

	// ZRange (sorted by score)
	members := eng.ZRange(key, 0, -1)
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}
	if string(members[0]) != "alice" || string(members[1]) != "bob" || string(members[2]) != "charlie" {
		t.Fatalf("unexpected order: %v", members)
	}

	// ZScore
	score, ok := eng.ZScore(key, []byte("bob"))
	if !ok || score != 2.0 {
		t.Fatalf("expected score 2.0, got %f", score)
	}

	// ZRem
	eng.ZRem(key, []byte("bob"))
	if card := eng.ZCard(key); card != 2 {
		t.Fatalf("expected 2 members after remove, got %d", card)
	}
}

func TestEngineFullTextSearch(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	eng.FTAdd("doc1", "Go is a fast compiled language")
	eng.FTAdd("doc2", "Python is great for data science")
	eng.FTAdd("doc3", "Go databases are very performant")

	results := eng.FTSearch("go", 10)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'go', got %d", len(results))
	}

	// doc1 and doc3 should match
	foundDoc1, foundDoc3 := false, false
	for _, r := range results {
		if r.DocID == "doc1" {
			foundDoc1 = true
		}
		if r.DocID == "doc3" {
			foundDoc3 = true
		}
	}
	if !foundDoc1 || !foundDoc3 {
		t.Fatalf("expected doc1 and doc3 in results, got %v", results)
	}
}

func TestEngineStats(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ignisdb-test-*")
	defer os.RemoveAll(dir)

	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	eng.Put([]byte("k1"), []byte("v1"))
	eng.Get([]byte("k1"))
	eng.Get([]byte("missing"))

	stats := eng.GetStats()
	writes := stats["writes"].(int64)
	reads := stats["reads"].(int64)
	if writes != 1 {
		t.Fatalf("expected 1 write, got %d", writes)
	}
	if reads != 2 {
		t.Fatalf("expected 2 reads, got %d", reads)
	}
}
