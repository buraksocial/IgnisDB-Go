.PHONY: build build-rust test bench clean run release

# Build the Rust core static library (required for CGo linkage)
build-rust:
	cd rust-core && cargo build --release

# Build the server binary (Rust core + Go)
build: build-rust
	CGO_ENABLED=1 go build -ldflags="-s -w" -o bin/ignisdb-go ./cmd/server

test: build-rust
	CGO_ENABLED=1 go test -v -race ./...

bench: build-rust
	CGO_ENABLED=1 go test -bench=. -benchmem ./benchmarks/

clean:
	rm -rf bin/ data/
	cd rust-core && cargo clean

run: build
	./bin/ignisdb-go

release: build-rust
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/ignisdb-go-linux-amd64 ./cmd/server
