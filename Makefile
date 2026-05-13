.PHONY: build test test-integration lint fmt clean

build:
	mkdir -p build
	go build -o build/tlsrpt-digest ./cmd/tlsrpt-digest

test:
	go test -v ./...

# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).
# See docs/tasks/0010_imap/03_implementation_plan.md section 5.2 for setup instructions.
test-integration:
	go test -v -tags integration ./internal/imap/...

lint:
	golangci-lint run
	golangci-lint run --config .golangci-security.yml

fmt:
	gofumpt -w .

clean:
	rm -rf build/
