# ADR-0002: Managing Credentials (IMAP Authentication / Slack Webhook URLs) via Environment Variables

| Item | Content |
|---|---|
| Number | ADR-0002 |
| Status | Adopted |
| Decision date | 2026-05-21 |
| Related tasks | 0060_config, 0070_entrypoint |

---

## 1. Context

tlsrpt-digest requires the following credentials.

| Information | Purpose |
|---|---|
| IMAP username | Authentication to the mail server |
| IMAP password | Authentication to the mail server |
| Slack Webhook URL (success) | Notification destination for periodic summaries |
| Slack Webhook URL (error) | Notification destination for alerts and errors |

A decision is needed on where to manage these (configuration file vs. environment variables vs. other).

This system is designed for one-shot execution via systemd timer or cron; it does not daemonize or accept interactive input.

---

## 2. Design Considerations

### Risks of Including Credentials in the Configuration File (TOML)

The TOML file manages application behavior parameters (connection host, retention period, notification hostname, etc.). Including this file in version control (e.g., Git) makes it easy for team members to share and review configuration.

However, including passwords or Webhook URLs in the same file introduces the following risks:

- If accidentally committed to the repository, the cost of removing them from the full history is high.
- Misconfigured file permissions (e.g., `chmod 644`) expose the file to other users.
- The risk of credentials leaking into log output or error messages increases (e.g., sensitive values appearing in parse error output).

### Rationale for `notify.slack.allowed_host` in TOML

When Webhook URLs are managed solely via environment variables, a misconfiguration or unintended injection (e.g., environment variable substitution) could cause notification data to be sent to an attacker-controlled server. TLSRPT reports contain information such as sending domains, policy types, and failure counts; if leaked, this would expose information about the organization's mail sending infrastructure to external parties.

By explicitly specifying `notify.slack.allowed_host` in TOML, the application can validate both Webhook URL hosts at startup and block requests to unexpected destinations at the application layer. Since the TOML file is subject to version control and review, changes to the allowed host are recorded as explicit configuration changes.

**Residual risk**: Because `allowed_host` only validates the host portion, substitution with a different Webhook path under the same host (e.g., `hooks.slack.com/services/ATTACKER_TOKEN`) cannot be prevented. This risk is accepted (preventing Webhook URL exposure itself is the responsibility of file permission management on `secrets.env`).

### Characteristics of Using Environment Variables

- systemd's `EnvironmentFile=` directive allows management via a dedicated file such as `/etc/tlsrpt-digest/secrets.env`.
- `secrets.env` can be managed with different permissions (`0600`) from the TOML configuration file, as a separate file.
- In container environments (Docker, Kubernetes), secret injection via environment variables is the standard approach.
- Less likely to be accidentally recorded in `slog`-based debug logs (via redaction using the `config.Secret` type; see existing `internal/config/secret.go`).
- For cron, an equivalent approach to `EnvironmentFile` (e.g., sourcing `. secrets.env` before execution) can be used.

---

## 3. Alternatives Considered

### Option A: Include in Configuration File (TOML)

Record IMAP username, password, and Webhook URLs in the TOML file.

**Reason for rejection**:

- High risk of accidental commit to version control.
- Cannot separate access permissions between configuration file and secrets.
- Sensitive values may appear in error messages on parse failure (the existing `internal/config.Load` wraps errors with `fmt.Errorf("config: decode failed: %w", err)`, and TOML decode errors may include field values).

### Option B: Manage via Environment Variables (Adopted)

Pass IMAP credentials and Webhook URLs as environment variables. Record only non-sensitive information required for connection validation (`notify.slack.allowed_host`) in the configuration file.

**Reason for adoption**: As described in Section 2, separating the lifecycle of credentials from the configuration file reduces risk. systemd's `EnvironmentFile=` also enables integration with existing operations tools (e.g., systemd-creds).

### Option C: External Secret Management System (e.g., HashiCorp Vault)

Retrieve secrets at runtime from a secret management system such as Vault.

**Reason for rejection**: This is over-engineered for the use case of this system (TLSRPT monitoring for individuals or small teams). It increases external service dependencies and raises availability requirements. Not adopted at this time per the YAGNI principle.

### Option D: Interactive Input at Startup

Accept the password from standard input at program startup.

**Reason for rejection**: Cannot be adopted because the system assumes automated execution (non-interactive environment) via systemd timer or cron.

---

## 4. Decision

Manage credentials (IMAP password, Slack Webhook URLs) and IMAP username via environment variables.

The configuration file (TOML) contains only the following information.

| Item | Location | Reason |
|---|---|---|
| IMAP hostname, port, mailbox name | TOML | Not sensitive; easy to share and version-control across environments |
| `notify.slack.allowed_host` | TOML | For validating the host portion of both Webhook URLs (success and error). Both URLs are assumed to share the same host, making this the common allowed host (contains no sensitive information) |
| IMAP username and password | Environment variables | Do not store authentication credentials in the TOML file |
| Slack Webhook URLs (2) | Environment variables | Sensitive information |

In systemd environments, manage via `EnvironmentFile=/etc/tlsrpt-digest/secrets.env`. Protect `secrets.env` with `0600` permissions.

### 4.1 Environment Variable Names and Behavior When Unset

| Environment variable | Purpose | Behavior when unset |
|---|---|---|
| `TLSRPT_IMAP_USERNAME` | IMAP authentication username | Error when executing subcommands that require IMAP access (e.g., `fetch`). Not required for subcommands that do not use IMAP (e.g., `summary`) |
| `TLSRPT_IMAP_PASSWORD` | IMAP authentication password | Error when executing subcommands that require IMAP access (e.g., `fetch`). Not required for subcommands that do not use IMAP (e.g., `summary`) |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | Webhook URL for periodic summary notifications | Slack notifications disabled (not an error) |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | Webhook URL for alert and error notifications | Slack notifications disabled (not an error) |

### 4.2 Prohibition in TOML

The `imap.username` / `imap.password` keys are not included in the TOML schema. With `pelletier/go-toml/v2`'s `DisallowUnknownFields()`, configuration files containing these keys will result in an error at startup. No deprecation period is provided; these keys are rejected from the start.

### 4.3 Scope of `config.Secret` Application

The `config.Secret` type (which redacts values in log output) is applied only to the following:

| Value | Type | Reason |
|---|---|---|
| `TLSRPT_IMAP_PASSWORD` | `config.Secret` | Leakage would allow authentication to be bypassed |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | `config.Secret` | Leakage would allow third parties to send notifications |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | `config.Secret` | Same as above |
| `TLSRPT_IMAP_USERNAME` | `string` | Visibility is useful during debugging. Often in email address format; acceptable for it to appear in logs |

---

## 5. Resulting Trade-offs

| Gained | Lost |
|---|---|
| The configuration file can be managed in a repository without leaking credentials | Two files (environment variables and configuration file) must be managed |
| `secrets.env` can be protected with independent permissions from the TOML file | The complete configuration picture does not fit in a single file (references are distributed) |
| The design aligns with secret injection in container environments | An implementation is needed to clearly communicate error messages when environment variables are unset |
| Risk of credentials leaking into logs and error messages is reduced at the design level | — |
