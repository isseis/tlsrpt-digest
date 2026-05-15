# Robustness Principle

## Overview

This project applies the **Robustness Principle** (also known as Postel's Law) as a system-wide design guideline:

> **Be conservative in what you do, be liberal in what you accept from others.**
>
> — Jon Postel, RFC 793 (TCP)

## What This Means in Practice

### Be conservative in what you send (output)

When this system produces output — sending Slack notifications, writing JSON files, formatting report data — it must conform strictly to the relevant specification. Do not produce ambiguous, non-standard, or under-specified output.

**Examples:**
- TLSRPT alert messages must include all required fields as defined in the requirements
- JSON files written by `internal/store` must be valid, well-formed JSON
- Outgoing email reports (future) must use the correct MIME types and structure

### Be liberal in what you accept (input)

When receiving data from external systems — email servers, TLSRPT report senders, configuration files edited by operators — tolerate reasonable variations that a strict parser would reject.

**Examples:**
- When identifying TLSRPT report attachments, prefer `Content-Type: application/tlsrpt+gzip` / `application/tlsrpt+json` (RFC 8460 MUST), but fall back to filename extension (`.json.gz` / `.json`) for senders that use a generic type like `application/octet-stream`
- When reading operator-edited TOML configuration, provide helpful error messages rather than silently failing

## Where This Applies in the Codebase

| Component | Conservative (output) | Liberal (input) |
|---|---|---|
| `internal/tlsrpt` | Validate required fields before returning `*Report` | `ParseGzip`/`ParseJSON` accept well-compressed data regardless of upstream sender |
| `internal/notify` | Send strictly formatted Slack messages | — |
| `cmd/tlsrpt-digest` | — | Dispatch TLSRPT attachments by Content-Type with filename fallback |

## When NOT to Apply

The liberal side of this principle applies to **external, uncontrolled inputs** (data from other organizations' email servers). It does **not** apply to:

- Data produced by this system and read back by this system (internal contracts should be strict)
- Security boundaries: never relax validation that prevents security vulnerabilities (e.g., decompressed size limits for zip bomb protection remain strict regardless of sender)
