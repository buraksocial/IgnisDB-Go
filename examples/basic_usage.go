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

	// Note: Sorted Sets and Full-Text Search are now handled by the RESP server layer,
	// not the embedded engine. Use the Redis-compatible server for ZADD/ZRANGE/FT.ADD/FT.SEARCH.

	// Stats
	stats := eng.GetStats()
	fmt.Printf("\nStats: %+v\n", stats)

	// Flush
	eng.Flush()
	fmt.Printf("After flush - SSTables: %d\n", eng.SSTableCount())
}
