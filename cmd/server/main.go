// IgnisDB-Go: High-performance LSM-Tree database server with RESP protocol.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
	"github.com/buraksocial/IgnisDB-Go/internal/server"
)

func main() {
	dataDir := flag.String("data-dir", "./data", "Database data directory")
	host := flag.String("host", "127.0.0.1", "Address to bind")
	port := flag.Int("port", 6380, "RESP port")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Open engine
	eng, err := engine.Open(engine.Config{
		DataDir: *dataDir,
	})
	if err != nil {
		log.Fatalf("Failed to open engine: %v", err)
	}

	// Create and start server
	addr := fmt.Sprintf("%s:%d", *host, *port)
	srv := server.New(eng, addr)

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		srv.Stop()
		eng.Close()
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
