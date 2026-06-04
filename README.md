# tlsrpt-digest

A tool that automatically fetches and parses SMTP TLS Reporting (RFC 8460) reports from an IMAP mailbox, sends an immediate alert to Slack when a TLS connection failure is detected, and sends a periodic summary under normal conditions.

## Table of Contents

- [Overview](#overview)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Configuration File (TOML)](#configuration-file-toml)
- [Environment Variables](#environment-variables)
- [Subcommands](#subcommands)
- [Scheduling](#scheduling)
- [Further Documentation](#further-documentation)

---

## Overview

Major email senders such as Google send daily RFC 8460-compliant JSON reports on the status of TLS policy enforcement (MTA-STS / DANE) on the receiving side. tlsrpt-digest automates the processing of these reports.

**Processing flow:**

1. Connect to the IMAP mailbox and fetch unprocessed report emails
2. Extract and parse the attached `.json.gz` files and evaluate `failure_session_count`
3. If a failure is detected → send an immediate alert to Slack
4. If no failure → accumulate data locally and send as a periodic summary

The program runs as a one-shot process and exits upon completion. Periodic execution is managed by a systemd timer or cron.

---

## Installation

### Build

Go 1.26 or later is required.

```bash
git clone https://github.com/isseis/tlsrpt-digest.git
cd tlsrpt-digest
make build
# Binary: ./build/tlsrpt-digest
```

### Note for Docker Containers

Minimal images (such as `ubuntu:24.04`) may not have `ca-certificates` installed. It is required for TLS connections to IMAP, so add the following to your Dockerfile:

```dockerfile
RUN apt-get update && apt-get install -y ca-certificates
```

---

## Quick Start

### 1. Create a configuration file

Create `config.toml` (see the next section for a template).

### 2. Set environment variables

```bash
export TLSRPT_IMAP_USERNAME="your-imap-username"
export TLSRPT_IMAP_PASSWORD="your-imap-password"
export TLSRPT_SLACK_WEBHOOK_URL_SUCCESS="https://hooks.slack.com/services/..."
export TLSRPT_SLACK_WEBHOOK_URL_ERROR="https://hooks.slack.com/services/..."
```

### 3. Run fetch

```bash
./build/tlsrpt-digest --config config.toml fetch
```

---

## Configuration File (TOML)

The configuration file path must always be specified with the `-c` / `--config` flag. Omitting it results in an error.

> **Note:** Do not write secrets such as passwords or webhook URLs in the configuration file. Pass them via [environment variables](#environment-variables) instead.

### Minimal Configuration

```toml
[imap]
host = "imap.example.com"
port = 993

[notify.slack]
allowed_host = "hooks.slack.com"
```

> **Note:** `[notify.slack] allowed_host` is required when Slack webhook URLs are set via environment variables. Omit the entire `[notify.slack]` section only if you are not using Slack notifications.

### All Configuration Items

```toml
[imap]
# IMAP server hostname (required)
host = "imap.example.com"

# IMAP server port number (required)
port = 993

# Mailbox name to monitor (default: "INBOX")
# Must be specified explicitly if TLSRPT reports are filtered to a dedicated folder.
# Gmail custom labels can be used as-is as folder names (no "[Gmail]/" prefix needed).
mailbox = "tls-reports"

# Lookback window in days for the fetch subcommand (default: 14)
fetch_days = 14

# Path to a custom CA certificate file (default: use system certificates)
# Set this when connecting to an IMAP server with a self-signed certificate.
tls_ca_cert = ""

# Maximum message size per email in bytes (default: 0 = unlimited)
max_message_bytes = 0

[notify.slack]
# Allowed hostname for Slack webhook URLs (must match the webhook URL hostname)
# Used as a security check to prevent notifications being sent to wrong destinations.
# Do not include a scheme or port number.
# Example: "hooks.slack.com"
allowed_host = "hooks.slack.com"

[store]
# Directory for storing report data (default: "./store")
root_dir = "/var/lib/tlsrpt-digest"

# Retention period for report JSON data in days (default: 30)
retention_days = 30

# Retention period for original emails (.eml files) in days (default: 30)
# Must be set to at least retention_days (original emails are needed for reprocessing).
max_email_age_days = 30

[summary]
# Lookback window in days for the summary subcommand (default: 7)
window_days = 7
```

---

## Environment Variables

Set secrets via the following environment variables:

| Environment Variable | Description |
|---|---|
| `TLSRPT_IMAP_PASSWORD` | IMAP authentication password |
| `TLSRPT_IMAP_USERNAME` | IMAP authentication username |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | Slack webhook URL for success (summary) notifications |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | Slack webhook URL for error (alert) notifications |

### Using Gmail

Google has discontinued password-based IMAP authentication. An **App Password** is required to use Gmail.

1. Google Account → Security → Enable 2-Step Verification
2. Security → App Passwords → Generate a password
3. Set the generated password as `TLSRPT_IMAP_PASSWORD`

---

## Subcommands

```
tlsrpt-digest --config <path> <fetch|summary|reprocess|gc|recover> [options]
tlsrpt-digest help
```

`--config` is a global flag and must be specified before the subcommand. Run `tlsrpt-digest help` to see detailed help.

### fetch

Fetches and processes reports from the IMAP mailbox.

```bash
tlsrpt-digest --config path fetch [--dry-run] [--since duration]
```

| Option | Description |
|---|---|
| `--dry-run` | Connects to IMAP and checks metadata without downloading, saving, or notifying |
| `--since duration` | Overrides the fetch window (e.g. `2d`, `1w`; unit: `d` for days or `w` for weeks). For `7d`, the window opens 7 days before today's UTC midnight onward, using today's UTC midnight as the endpoint. |

### summary

Aggregates accumulated reports and sends a periodic summary.

```bash
tlsrpt-digest --config path summary [--dry-run] [--window duration]
```

| Option | Description |
|---|---|
| `--window duration` | Overrides the summary window (e.g. `7d`, `2w`; unit: `d` for days or `w` for weeks). For `7d`, the window covers from 7 days before today's UTC midnight to today's UTC midnight. |

### gc

Deletes data older than the configured retention period.

```bash
tlsrpt-digest --config path gc [--before duration] [--max-email-age duration]
```

### reprocess

Re-parses stored `.eml` files. Use this after changing notification settings or after an error during initial processing.

```bash
tlsrpt-digest --config path reprocess [--notify]
```

### recover

Checks and repairs store integrity.

```bash
tlsrpt-digest --config path recover [--mode keep-old|discard-old] [--yes]
```

---

## Scheduling

tlsrpt-digest has no built-in scheduling capability. Use a systemd timer or cron for periodic execution.

### Using systemd timers

Set up two timers: one for `fetch` and one for `summary`.

**`/etc/systemd/system/tlsrpt-digest-fetch.service`**

```ini
[Unit]
Description=tlsrpt-digest fetch

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml fetch
EnvironmentFile=/etc/tlsrpt-digest/env
User=tlsrpt
```

**`/etc/systemd/system/tlsrpt-digest-fetch.timer`**

```ini
[Unit]
Description=tlsrpt-digest fetch timer

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
```

> Setting `Persistent=true` causes missed executions during system downtime to be run upon restart.

**`/etc/systemd/system/tlsrpt-digest-summary.service`**

```ini
[Unit]
Description=tlsrpt-digest summary

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml summary
EnvironmentFile=/etc/tlsrpt-digest/env
User=tlsrpt
```

**`/etc/systemd/system/tlsrpt-digest-summary.timer`**

```ini
[Unit]
Description=tlsrpt-digest summary timer

[Timer]
OnCalendar=Mon *-*-* 09:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

Environment variable file **`/etc/tlsrpt-digest/env`**:

```
TLSRPT_IMAP_USERNAME=your-imap-username
TLSRPT_IMAP_PASSWORD=your-imap-password
TLSRPT_SLACK_WEBHOOK_URL_SUCCESS=https://hooks.slack.com/services/...
TLSRPT_SLACK_WEBHOOK_URL_ERROR=https://hooks.slack.com/services/...
```

Enable the timers:

```bash
systemctl daemon-reload
systemctl enable --now tlsrpt-digest-fetch.timer
systemctl enable --now tlsrpt-digest-summary.timer
```

### Using cron

Secrets are loaded via wrapper scripts, avoiding plain-text credentials in the crontab file.

The environment variable file is the same **`/etc/tlsrpt-digest/env`** used for systemd (protect with `chmod 600`, `chown root`).

Wrapper script **`/usr/local/bin/tlsrpt-digest-fetch`**:

```sh
#!/bin/sh
set -eu
. /etc/tlsrpt-digest/env
exec /usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml fetch
```

Wrapper script **`/usr/local/bin/tlsrpt-digest-summary`**:

```sh
#!/bin/sh
set -eu
. /etc/tlsrpt-digest/env
exec /usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml summary
```

Make the scripts executable:

```bash
chmod 755 /usr/local/bin/tlsrpt-digest-fetch
chmod 755 /usr/local/bin/tlsrpt-digest-summary
```

crontab (edit with `crontab -e`):

```crontab
# Run fetch every hour
0 * * * * /usr/local/bin/tlsrpt-digest-fetch

# Send summary every Monday at 09:00
0 9 * * 1 /usr/local/bin/tlsrpt-digest-summary
```

---

## Further Documentation

| Document | Content |
|---|---|
| [Project Overview](docs/overview.md) | Architecture, processing flow, and design decisions |
| [Package Reference](docs/dev/developer_guide/package_reference.md) | Responsibilities and internal structure of each package |

---

## Contributing

For the development workflow from environment setup to PR merge, see the [Developer Onboarding Guide](docs/dev/developer_guide/development_process.ja.md).

---

## License

[MIT License](LICENSE)
