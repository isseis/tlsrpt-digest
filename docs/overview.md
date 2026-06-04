# tlsrpt-digest Project Overview

## 1. Purpose and Background

### What is TLSRPT

SMTP TLS Reporting (RFC 8460, commonly known as TLSRPT) is a specification by which email senders report the status of TLS policy enforcement (MTA-STS and DANE) on the receiving side. Major email senders such as Google send these reports daily as JSON files (gzip-compressed) attached to emails.

### Why Automated Processing is Needed

TLSRPT reports arrive in large volumes every day, making manual review impractical. What matters is whether `failure_session_count` (the number of TLS connection failures) is non-zero. When failures are detected, administrators must be notified promptly. Routine reports with no issues are accumulated and reported as a periodic summary, minimizing management overhead.

### Project Purpose

tlsrpt-digest automates the following:

1. Fetching report emails by connecting to an IMAP mailbox
2. Parsing the attached JSON and evaluating failure_session_count
3. Sending immediate alerts when failures are detected
4. Accumulating data on normal days and sending periodic summary notifications

---

## 2. Processing Flow

```mermaid
flowchart TD
    A[("IMAP mailbox")]
    B["Fetch message metadata (all in window)<br>internal/imap"]
    C["Extract attachment<br>.json.gz → JSON"]
    D["Parse RFC 8460 JSON<br>internal/tlsrpt"]
    E{"failure_session_count > 0?"}
    F["Send immediate alert<br>internal/notify"]
    G[("Stored reports / .eml<br>internal/store")]
    H["Generate and send periodic summary<br>internal/notify"]

    A --> B
    B --> C
    C --> D
    D --> E
    E -- "Yes (failure detected)" --> F
    E -- "No (no failure)" --> G
    G --> H
```

### Execution Model

The program runs as a one-shot process and exits after completing its work. Periodic execution is delegated to an external scheduler (systemd timer or cron).

```mermaid
flowchart LR
    S["External scheduler<br>systemd timer / cron"]
    Poll["fetch subcommand<br>Fetch and process messages"]
    Summary["summary subcommand<br>Send periodic summary"]

    S -->|"Periodic execution (e.g. hourly)"| Poll
    S -->|"Periodic execution (e.g. every Monday)"| Summary
```

---

## 3. Package Structure and Responsibilities

```
tlsrpt-digest/
├── cmd/        # Command-line entry points
├── internal/   # Core implementation
├── testdata/   # Real test data (.eml, .json.gz)
└── docs/       # Documentation
```

For detailed responsibilities of each package, see the [Package Reference](dev/developer_guide/package_reference.md).

---

## 4. Technical Decisions and Rationale

### Adopting the IMAP Polling Approach

The rationale for adopting IMAP polling instead of the Postfix pipe approach:

| Aspect | IMAP Polling | Postfix Pipe |
|---|---|---|
| Impact on Postfix | **None** (no configuration changes required) | Requires changes to the Postfix container configuration |
| Process management | Can be managed as an **independent process** | Tightly coupled to Postfix |
| Testability | **High** (interface mocks such as `FakeMailFetcher`) | Low |
| Reprocessing | Controllable via read/unread flags | One-time only |

### Interface-Driven Design

By defining interfaces such as `MailFetcher` and implementing notifications as a `slog.Handler` with typed event helpers, the design allows test doubles (`FakeMailFetcher`, spy handler) to be substituted during testing.

### Adopting File-Based Storage for Data Accumulation

Report data must be accumulated for the periodic summary. To operate without an external database server, the project stores aggregated report data as JSON files and preserves original emails as `.eml` files for reprocessing.

---

## 5. Notification Specification

### Immediate Alert (upon failure detection)

- **Trigger**: When a report with `failure_session_count > 0` is detected
- **Timing**: Immediately after processing the report (real-time)
- **Content**: Sending organization name, target policy (MTA-STS / DANE), failure count, report period
- **Notification destination**: Slack Webhook (preferred) or email

### Periodic Summary (normal operation)

- **Trigger**: Periodic schedule (e.g., every Monday)
- **Content**: Aggregation of reports received during the past week (success counts by domain and by policy)
- **Purpose**: Provides regular confirmation that the system is operating correctly even in weeks with no issues
- **Notification destination**: Same as for immediate alerts

---

## 6. Configuration Items

The configuration file uses TOML format.

### IMAP Connection Settings

| Item | Description | Example |
|---|---|---|
| `imap.host` | IMAP server hostname | `"imap.example.com"` |
| `imap.port` | IMAP server port number | `993` |
| `imap.username` | Authentication username | `"tlsrpt@example.com"` |
| `imap.password` | Authentication password | `"secret"` |
| `imap.mailbox` | Mailbox name to monitor | `"INBOX"` |
| `imap.fetch_days` | Lookback window in days for `fetch` processing | `14` |

Scheduling is controlled by an external scheduler such as `systemd timer` or `cron`; the application itself does not provide `polling.*` configuration.

### Notification Settings

| Item | Description | Example |
|---|---|---|
| `notify.slack.allowed_host` | Allowed Slack Webhook host name | `"hooks.slack.com"` |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | Success notification Webhook URL (environment variable) | `"https://hooks.slack.com/..."` |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | Error notification Webhook URL (environment variable) | `"https://hooks.slack.com/..."` |
| `notify.email.smtp_host` | SMTP host for email sending | `"smtp.example.com"` |
| `notify.email.from` | Sender email address | `"alert@example.com"` |
| `notify.email.to` | Recipient email address(es) | `["admin@example.com"]` |

---

## 7. Dependencies

| Library | Purpose |
|---|---|
| `emersion/go-imap` | IMAP client |
| `stretchr/testify` | Test assertions |
| TOML library (e.g., `BurntSushi/toml`) | Configuration file loading |
