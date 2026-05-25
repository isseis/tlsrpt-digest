# Inter-Process Lock Design Guidelines

## Overview

This project has multiple CLI subcommands that read and write the same store. In particular, `recover --mode discard-old --yes` and `recover --abort-reset --yes` are destructive operations that move and delete multiple files; if multiple writers run concurrently, the states of the reset manifest, staging, and sentinel will conflict.

This guideline defines the design policy for clearly separating the responsibilities of inter-process locks and maintaining the same assumptions in future implementations and extensions.

---

## 1. Lock Types and Responsibilities

| Lock | Target | Purpose | Held During |
|---|---|---|---|
| store-wide process lock | writer vs writer | Prevent concurrent writes among `fetch` / `gc` / `reprocess` / `recover` | For write subcommands: from before store open until processing is complete |
| summary consistency guard | summary vs recovery_required writer | Prevent `summary` from notifying based on a stale "no recovery needed" judgment | While `summary` is aggregating and checking whether to send, and while the writer is updating recovery_required |

These two types are not alternatives to each other. The store-wide process lock serializes writers against each other. The summary consistency guard synchronizes only between `summary` (which does not acquire the store-wide process lock) and writers that modify recovery_required.

---

## 2. Store-Wide Process Lock Contract

Write subcommands acquire a non-blocking exclusive `flock` on `{root_dir}/.tlsrpt-digest-store.lock` before opening the store. If acquisition fails, another process is considered to be writing to the same store, and the subcommand fails immediately without waiting.

Target subcommands:

- `fetch`
- `gc`
- `reprocess`
- `recover --mode keep-old`
- `recover --mode discard-old` (including dry-run without `--yes`)
- `recover --mode discard-old --yes`
- `recover --abort-reset --yes`

Preconditions:

1. All writers use the same lock path.
2. The lock is acquired before `store.Open(...)`.
3. The lock handle is held until the subcommand's processing is complete.
4. If `store.Open(...)` fails, the lock handle is closed immediately; on success, it is held until processing is complete.
5. `recover --mode discard-old --yes` and `recover --abort-reset --yes` use `OpenRecoverReset` while holding the lock.
6. When CLI commands or internal tools call `ResetForRecovery` / `AbortReset` directly, the same writer lock must be held.
7. When `internal/store` unit tests call `ResetForRecovery` / `AbortReset` directly, an OS-level writer lock is not required, but the single-writer assumption must be made explicit (e.g., by calling from a single goroutine).

`ResetForRecovery` / `AbortReset` execute from manifest read through cleanup under the assumption of a single writer. Therefore, callers must hold the store-wide process lock to use these APIs safely.

---

## 3. Summary Consistency Guard Contract

`summary` does not acquire the store-wide process lock. This is by design to allow it to run concurrently with `fetch`. Instead, `summary` acquires a shared lock via `AcquireSummaryConsistencyGuard()` and maintains a boundary within which the appearance of `recovery_required` can be detected, up until just before sending. The guard file path is `{root_dir}/.tlsrpt-digest-summary.lock`.

On the writer side, an exclusive lock on the summary consistency guard is required only for operations that create or clear `recovery_required`.

Target:

- `SaveRecoveryRequired`
- `ClearRecoveryRequired`
- `ApplyRecovery`
- Commit processing within `ResetForRecovery` (`commitReset`)

Not a target:

- Initial manifest/staging creation in `ResetForRecovery`
- `stageDataFile`
- `stageEmailsDir`
- Restore processing in `AbortReset`
- Post-commit cleanup

Operations not listed as targets do not modify `recovery_required`, so they do not need to be synchronized with summary's "no recovery needed" judgment. Conflicts between writers are handled by the store-wide process lock.

---

## 4. Policy for Avoiding Over-Protection

The summary consistency guard must not be used as a substitute for the store-wide writer lock. The guard is a lock for protecting the visibility of `recovery_required`; it does not serialize the entire state machine of the reset manifest or staging.

Examples to avoid:

- Wrapping only manifest creation in the summary guard while leaving subsequent staging / commit / cleanup unprotected
- Wrapping operations that do not modify `recovery_required` in the summary guard for long periods, unnecessarily blocking `summary`
- Conflating the responsibilities of the store-wide process lock and the summary guard through shared comments or API names

Preferred separation:

- writer vs writer: store-wide process lock at the cmd layer
- summary vs recovery_required writer: summary consistency guard in `internal/store`
- crash recovery: reset manifest, staging, sentinel commit barrier

---

## 5. Implementation and Review Checklist

When adding or modifying write subcommands:

- [ ] The store-wide process lock is acquired before opening the store
- [ ] The lock handle is held until processing is complete
- [ ] The lock handle is closed even on abnormal exit paths
- [ ] `recover --mode discard-old --yes` / `recover --abort-reset --yes` uses `OpenRecoverReset` while holding the lock
- [ ] When `ResetForRecovery` / `AbortReset` is called directly from CLI commands or internal tools, the same writer lock is held
- [ ] When `ResetForRecovery` / `AbortReset` is called directly in `internal/store` unit tests, the single-writer assumption (e.g., single goroutine) is made explicit

When adding or modifying store APIs that modify `recovery_required`:

- [ ] An exclusive lock on `{root_dir}/.tlsrpt-digest-summary.lock` is acquired (using `withGuardExclusive`)
- [ ] Operations that do not modify `recovery_required` are not wrapped in the summary guard
- [ ] It is tested that `summary` does not send notifications based on a stale "no recovery needed" judgment

---

## 6. Related Documents

- `docs/dev/adr/0003_reset_phase_design.ja.md`
- `docs/tasks/0070_entrypoint/02_architecture.md` §3.3 / §6.4
- `docs/tasks/0070_entrypoint/03_implementation_plan.md` Step 1-5 / 3-3
