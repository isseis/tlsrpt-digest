# Package Structure Reference

This document provides a detailed reference for the package structure in this codebase.

## Directory Structure

- `cmd/`: Command-line entry points
  - `tlsrpt-digest/`: Main binary
- `internal/`: Core implementation
  - `config/`: Shared configuration types and TOML loading
  - `imap/`: IMAP client
    - `testutil/`: Test doubles for the imap package
  - `mailparse/`: MIME attachment extraction
  - `tlsrpt/`: TLSRPT report parsing
  - `notify/`: Notification sending
    - `testutil/`: Test doubles for the notify package
  - `store/`: Data persistence
    - `testutil/`: Test doubles for the store package
  - `storelock/`: Store write process lock
- `docs/`: Project documentation
- `testdata/`: Test data files

## Package Responsibilities

### Entry Point (`cmd/`)

- **`tlsrpt-digest/`**: One-shot binary that reads TOML configuration, initializes components, and runs one processing cycle. Scheduling is delegated to an external scheduler (systemd timer / cron). Processing is separated by the following subcommands:
  - `fetch`: Fetches and processes reports from IMAP; sends an alert when a failure is detected
  - `summary`: Aggregates accumulated reports and sends a periodic summary
  - `reprocess`: Re-parses stored `.eml` files and rebuilds the report store
  - `gc`: Deletes report data and `.eml` files that have exceeded the retention period
  - `recover`: Checks and repairs store integrity when a UIDVALIDITY change is detected

### Core Packages (`internal/`)

#### Mail Fetching

- **`imap/`**: Defines the `MailFetcher` interface and implements TLS connection to an IMAP server, metadata retrieval, selective message download, and marking messages as read.
- **`imap/testutil/`**: Provides a test double for `MailFetcher` (`FakeMailFetcher`). Has spy functionality (call recording). Package name is `imaptestutil`.

#### MIME Parsing

- **`mailparse/`**: Extracts attachment byte slices and filenames from `*mail.Message`. Sits between `imap` and `tlsrpt`, separating MIME parsing concerns from both packages.

#### TLSRPT Parsing

- **`tlsrpt/`**: Decompresses `.json.gz` byte slices, parses RFC 8460 JSON, and evaluates `failure_session_count`.

#### Notification

- **`notify/`**: Sends notifications to Slack Incoming Webhooks via `SlackHandler` (a `slog.Handler` implementation). Routes INFO-level normal summaries and WARN/ERROR-level alerts to separate webhook destinations. Records are buffered and delivered on `Flush()`. Webhook URLs are managed as environment variables. `BuildHandlers` constructs handlers and validates webhook URL hostnames.
- **`notify/testutil/`**: Provides a spy implementation of `slog.Handler` (`SpyHandler`). Records received log records for use in verifying notification content in tests. Package name is `notifytestutil`.

#### Data Persistence

- **`store/`**: Persists processed report data as JSON files (for periodic summary generation). Saves raw incoming mail as `.eml` files (for problem analysis, reprocessing, and test fixtures). `SummaryConsistencyGuard` uses a shared lock to ensure consistency with the summary subcommand. Manages recovery state for UIDVALIDITY changes via sentinel files.
- **`store/testutil/`**: Provides an in-memory store implementation (`FakeStore`). Used for injecting and verifying store operations in tests. Package name is `storetestutil`.

#### Process Lock

- **`storelock/`**: Provides a process-wide exclusive lock for store write operations. Write subcommands (`fetch`, `gc`, `reprocess`, `recover`) must acquire the lock via `Acquire` before calling `store.Open`, and hold it until processing is complete.

#### Shared Types

- **`config/`**: Provides TOML configuration loading and type definitions. `Secret` always returns a masked value from `String()` / `LogValue()` to prevent leaking secrets into logs. `Load` / `LoadFile` perform strict validation (unknown key rejection and field value validation).

## Key Design Patterns

- **Separation of concerns**: Each package has a single responsibility
- **Interface-based design**: Heavy use of interfaces for testability (e.g., `MailFetcher`)
- **One-shot execution**: The binary runs one cycle (start, process, exit) with no internal scheduling
- **Error handling**: Comprehensive error types and validation
