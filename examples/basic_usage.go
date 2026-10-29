// Example: Basic usage of IgnisDB-Go embedded engine.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
)

func main() {
	dir := "./example_data"
	defer os.RemoveAll(dir)

	// Open database
	eng, err := engine.Open(engine.Config{DataDir: dir})
	if err != nil {
		log.Fatal(err)
	}
	defer eng.Close()

	// SET
	eng.Put([]byte("name"), []byte("IgnisDB-Go"))
	eng.Put([]byte("version"), []byte("2.0.0"))
	eng.Put([]byte("language"), []byte("Go"))

	// GET
	if val, ok := eng.Get([]byte("name")); ok {
		fmt.Printf("name = %s\n", val)
	}

	// DELETE
	eng.Delete([]byte("version"))
	if _, ok := eng.Get([]byte("version")); !ok {
		fmt.Println("version deleted successfully")
	}

	// Sorted Sets
	eng.ZAdd([]byte("leaderboard"), 100, []byte("alice"))
	eng.ZAdd([]byte("leaderboard"), 200, []byte("bob"))
	eng.ZAdd([]byte("leaderboard"), 150, []byte("charlie"))

	members := eng.ZRange([]byte("leaderboard"), 0, -1)
	fmt.Println("Leaderboard:")
	for _, m := range members {
		score, _ := eng.ZScore([]byte("leaderboard"), m)
		fmt.Printf("  %s: %.0f\n", m, score)
	}

	// Full-Text Search
	eng.FTAdd("doc1", "Go is a fast compiled language")
	eng.FTAdd("doc2", "Python is great for data science")
	eng.FTAdd("doc3", "Go databases are very performant")

	results := eng.FTSearch("go language", 10)
	fmt.Println("\nSearch results for 'go language':")
	for _, r := range results {
		fmt.Printf("  %s (score: %.4f)\n", r.DocID, r.Score)
	}

	// Stats
	stats := eng.GetStats()
	fmt.Printf("\nStats: %+v\n", stats)
}
