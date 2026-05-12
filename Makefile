.PHONY: build test lint fmt clean

build:
	mkdir -p build
	go build -o build/tlsrpt-digest ./cmd/tlsrpt-digest

test:
	go test -v ./...

lint:
	golangci-lint run
	golangci-lint run --config .golangci-security.yml

fmt:
	gofumpt -w .

clean:
	rm -rf build/
