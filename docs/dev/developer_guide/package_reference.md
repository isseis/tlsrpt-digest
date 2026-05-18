# Package Structure Reference

This document provides a detailed reference for the package structure in this codebase.

## Directory Structure

- `cmd/`: Command-line entry points
  - `tlsrpt-digest/`: Main binary
- `internal/`: Core implementation
  - `config/`: Shared configuration types
  - `imap/`: IMAP client
    - `testutil/`: Test doubles for the imap package
  - `mailparse/`: MIME attachment extraction (planned)
  - `tlsrpt/`: TLSRPT report parsing (planned)
  - `notify/`: Notification sending (planned)
  - `store/`: Data persistence (planned)
- `docs/`: Project documentation
- `testdata/`: Test data files (planned)

## Package Responsibilities

### Entry Point (`cmd/`)

- **`tlsrpt-digest/`**: One-shot binary that reads TOML configuration, initializes components, and runs one processing cycle before exiting. Scheduling is delegated to an external scheduler (systemd timer / cron). Subcommands `fetch` / `summary` / `reprocess` separate processing concerns (planned).

### Core Packages (`internal/`)

#### Mail Fetching

- **`imap/`**: Defines the `MailFetcher` interface and implements TLS connection to an IMAP server, metadata retrieval, selective message download, and marking messages as read.
- **`imap/testutil/`**: Provides a test double for `MailFetcher` (`FakeMailFetcher`). Records calls (spy functionality). Package name is `imaptestutil`.

#### MIME Parsing (planned)

- **`mailparse/`**: Extracts attachment byte slices and filenames from `*mail.Message`. Sits between `imap` and `tlsrpt`, separating MIME parsing concerns from both packages.

#### TLSRPT Parsing (planned)

- **`tlsrpt/`**: Decompresses `.json.gz` byte slices, parses RFC 8460 JSON, and evaluates `failure_session_count`.

#### Notification (planned)

- **`notify/`**: Defines the `Notifier` interface and implements immediate alert and periodic summary delivery via Slack Incoming Webhook. Supports separate channels for INFO and WARN/ERROR severity. Webhook URLs are managed as environment variables.

#### Data Persistence (planned)

- **`store/`**: Persists processed report data as JSON files (for periodic summary generation). Saves raw incoming mail as `.eml` files (for problem analysis, reprocessing, and test fixtures).

#### Shared Types

- **`config/`**: Defines types shared across multiple packages, including `Secret`. `Secret` always returns a masked value from `String()` / `LogValue()` to prevent leaking sensitive data in logs.

## Key Design Patterns

- **Separation of concerns**: Each package has a single responsibility
- **Interface-based design**: Heavy use of interfaces for testability (e.g., `MailFetcher`, `Notifier`)
- **One-shot execution**: The binary runs one cycle (start, process, exit) with no internal scheduling
- **Error handling**: Comprehensive error types and validation
