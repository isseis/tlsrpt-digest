.PHONY: build test test-integration lint fmt deadcode clean

build:
	mkdir -p build
	go build -o build/tlsrpt-digest ./cmd/tlsrpt-digest

test:
	go test -v -tags test ./...

# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).
# See docs/tasks/0010_imap/03_implementation_plan.md section 5.2 for setup instructions.
test-integration:
	go test -v -count=1 -tags test,integration ./internal/imap/...

lint:
	golangci-lint run --build-tags test --timeout=5m
	golangci-lint run --config .golangci-security.yml --build-tags test --timeout=5m

fmt:
	gofumpt -w .

deadcode:
	deadcode -test ./cmd/tlsrpt-digest

clean:
	rm -rf build/
