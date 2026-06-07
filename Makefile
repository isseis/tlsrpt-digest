.PHONY: build test test-integration test-slack-notify lint fmt deadcode clean

build:
	mkdir -p build
	go build -o build/tlsrpt-digest ./cmd/tlsrpt-digest

test:
	go test -v -tags test ./...

# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).
# Covers internal/imap and cmd/tlsrpt-digest integration tests.
test-integration:
	go test -v -count=1 -tags test,integration ./internal/imap/... ./cmd/tlsrpt-digest/...

# Manually send a real Slack alert from testdata to verify webhook
# connectivity and message formatting. Requires
# TLSRPT_SLACK_WEBHOOK_URL_ERROR; skipped when unset.
test-slack-notify:
	go test -v -count=1 -tags test,slack_notify -run ^TestSlackNotify ./cmd/tlsrpt-digest/...

lint:
	golangci-lint run --build-tags test --timeout=5m
	golangci-lint run --config .golangci-security.yml --build-tags test --timeout=5m
	golangci-lint run --build-tags test,slack_notify --timeout=5m
	golangci-lint run --config .golangci-security.yml --build-tags test,slack_notify --timeout=5m

fmt:
	gofumpt -w .

deadcode:
	deadcode -test -tags test ./cmd/tlsrpt-digest

clean:
	rm -rf build/
