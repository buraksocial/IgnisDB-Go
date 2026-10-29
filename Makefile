.PHONY: build test bench clean release run

build:
	go build -ldflags="-s -w" -o bin/ignisdb-go ./cmd/server

test:
	go test -v -race ./...

bench:
	go test -bench=. -benchmem ./benchmarks/

clean:
	rm -rf bin/ data/

run: build
	./bin/ignisdb-go

release:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/ignisdb-go-linux-amd64 ./cmd/server
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/ignisdb-go-darwin-arm64 ./cmd/server
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/ignisdb-go-windows-amd64.exe ./cmd/server
