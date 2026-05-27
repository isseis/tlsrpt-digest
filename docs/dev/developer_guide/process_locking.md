# Inter-Process Lock Design Guidelines

## Overview

This project has multiple CLI subcommands that read and write the same store.
To prevent inconsistencies from concurrent execution, two types of locks with different purposes are used.

| Lock | Problem solved |
|---|---|
| store-wide process lock | Prevents concurrent execution among write subcommands |
| summary consistency guard | Prevents `recovery_required` from being missed during concurrent execution of `summary` and `fetch` |

These two are not alternatives to each other; each solves an independent problem.

---

## 1. Subcommand Concurrent Execution Compatibility

| | fetch | gc | reprocess | recover | summary |
|---|---|---|---|---|---|
| **fetch** | ✗ | ✗ | ✗ | ✗ | ○ |
| **gc** | ✗ | ✗ | ✗ | ✗ | ○ |
| **reprocess** | ✗ | ✗ | ✗ | ✗ | ○ |
| **recover** | ✗ | ✗ | ✗ | ✗ | ○ |
| **summary** | ○ | ○ | ○ | ○ | ○ |

`fetch`, `gc`, `reprocess`, and `recover` hold the store-wide process lock (exclusive) and therefore
cannot run concurrently with each other. `summary` does not acquire the store-wide process lock and
can run concurrently with write subcommands.

Concurrent execution of multiple `summary` instances is permitted by the summary consistency guard (shared lock) and poses no problem.

---

## 2. Store-Wide Process Lock

### Purpose

Serialize write subcommands against each other so that the state machine of the reset manifest,
staging, and sentinel can be operated safely under the single-writer assumption.

The state machine here refers to the mechanism that tracks the progress of recovery operations
(`ResetForRecovery` / `AbortReset`) triggered by UIDVALIDITY changes. It consists of three elements:
the reset manifest (a progress ledger recording `resetPhase` values 1–5), the staging directory,
and the sentinel (recording the committed state of `recovery_required` and `UIDValidity`).
See [ADR-0003](../adr/0003_reset_phase_design.md) for details.

### Lock File

`{root_dir}/.tlsrpt-digest-store.lock` (exclusive flock, non-blocking)

If acquisition fails, another process is considered to be writing to the same store, and the subcommand fails immediately without waiting.

### Target Subcommands

- `fetch`
- `gc`
- `reprocess`
- `recover` (any of `--mode keep-old` / `discard-old` / `--abort-reset`)

### Contract

1. Acquire before opening the store (`store.Open(...)` call).
2. Hold until processing is complete (including abnormal exit paths).
3. `recover --mode discard-old --yes` / `recover --abort-reset --yes` uses `OpenRecoverReset` while holding the lock.
4. Since `ResetForRecovery` / `AbortReset` are designed under the single-writer assumption, callers must always hold the store-wide process lock.
5. When called directly from `internal/store` unit tests, an OS-level lock is not required, but the single-writer assumption must be made explicit (e.g., a single goroutine).

---

## 3. Summary Consistency Guard

### Why It Is Needed

`summary` does not acquire the store-wide process lock because it is designed to run concurrently with `fetch`. `fetch` writes the `recovery_required` sentinel when it detects a UID validity change. If `summary` sends aggregated results without noticing this write, an inconsistent summary will be delivered.

The summary consistency guard prevents this.

### Lock File and Lock Type

Lock file: `{root_dir}/.tlsrpt-digest-summary.lock`

| Acquirer | flock type | Behavior on failure |
|---|---|---|
| `summary` (`AcquireSummaryConsistencyGuard`) | shared (`LOCK_SH\|LOCK_NB`) | Error exit |
| Store APIs that modify `recovery_required` (`withGuardExclusive`) | exclusive (`LOCK_EX`) | Block (wait) |

While `summary` holds the shared lock, a `fetch` that attempts to write to the `recovery_required`
sentinel blocks on exclusive lock acquisition. `fetch` does not error out; it waits until `summary`
releases the shared lock. The block occurs only at the `SaveRecoveryRequired` call site; prior
operations such as mail fetching and report saving proceed concurrently with `summary`.

### Store APIs That Modify `recovery_required` (Exclusive Lock Required)

- `SaveRecoveryRequired`
- `ClearRecoveryRequired`
- `ApplyRecovery`
- Commit processing in `ResetForRecovery` (`commitReset`)

The following do not modify `recovery_required` and do not require the guard:

- Initial manifest/staging creation in `ResetForRecovery`
- `stageDataFile` / `stageEmailsDir`
- Restore processing in `AbortReset`
- Post-commit cleanup

### Only `fetch` Calls `SaveRecoveryRequired`

`SaveRecoveryRequired` is currently called only by `fetch`. `gc`, `reprocess`, and `recover` can
run concurrently with `summary` but do not write the `recovery_required` sentinel, so they are
outside the scope of the summary consistency guard.

**The only concurrency the summary consistency guard addresses is concurrent execution with `fetch`.**

---

## 4. Summary `recovery_required` Check Design

### Scope of the Shared Lock

The shared lock is acquired during Bootstrap (`AcquireSummaryConsistencyGuard`) and held until
`guard.Close()` (`boot.Close()`). That is, it is held throughout the entire execution of the
`summary` command.

During this time, `fetch`'s `SaveRecoveryRequired` is blocked from acquiring the exclusive lock,
making it **physically impossible for the sentinel to be written during `summary` execution**.

The only race window is the timing at which `fetch` writes the sentinel before Bootstrap acquires the shared lock.

### Check Timing and Purpose

`summary` calls `CheckRecoveryRequired` exactly once, before aggregation begins. This is to detect
any sentinel that was written before the shared lock was acquired.

```
Bootstrap: acquire shared lock
           CheckRecoveryRequired   ← detects writes that occurred before shared lock acquisition
               ↓ found=true: notify and exit
           GenerateSummary (store read)
               ↓ ReportCount == 0: exitOK
           buildNotifier
           LogSummary / Flush (Slack send)
boot.Close(): release shared lock
           fetch: sentinel write becomes possible here
```

If `recovery_required` is set, the store data will be entirely deleted by the subsequent `recover`.
Sending a summary of data that is about to be wiped would only cause confusion, so the command notifies and exits.

### Why Not Re-check Just Before Sending

Since `fetch`'s sentinel write is blocked while `summary` holds the shared lock, the sentinel cannot
change after `CheckRecoveryRequired` passes. A re-check is unnecessary.

---

## 5. Policy for Avoiding Over-Protection

The summary consistency guard must not be used as a substitute for the store-wide process lock.
The guard protects only the visibility of `recovery_required`; it does not serialize the entire
state machine of the manifest or staging.

Patterns to avoid:

- Wrapping operations that do not modify `recovery_required` in the summary guard, unnecessarily blocking `summary`
- Wrapping only manifest creation in the summary guard while leaving subsequent staging / commit / cleanup unprotected
- Conflating the responsibilities of the two lock types through shared comments or API names

Preferred responsibility split:

| Problem | Solution |
|---|---|
| Serialization of write subcommands against each other | store-wide process lock (cmd layer) |
| `summary` vs `fetch` `recovery_required` race | summary consistency guard (`internal/store` layer) |
| Atomicity of crash recovery | reset manifest, staging, sentinel commit barrier |

---

## 6. Implementation and Review Checklist

**When adding or modifying write subcommands**

- [ ] The store-wide process lock is acquired before opening the store
- [ ] The lock handle is held until processing is complete (including abnormal exit paths)
- [ ] `recover --mode discard-old --yes` / `recover --abort-reset --yes` uses `OpenRecoverReset`
  while holding the lock
- [ ] `ResetForRecovery` / `AbortReset` is called while holding the store-wide process lock
- [ ] The single-writer assumption is made explicit when called directly from `internal/store`
  unit tests
- [ ] When newly calling `SaveRecoveryRequired`, the contract in section 3 is followed and
  consistency with the summary consistency guard is verified

**When adding or modifying store APIs that modify `recovery_required`**

- [ ] An exclusive lock on `{root_dir}/.tlsrpt-digest-summary.lock` is acquired
  (using `withGuardExclusive`)
- [ ] Operations that do not modify `recovery_required` are not wrapped in the summary guard
- [ ] It is tested that `summary` does not send based on a stale "no recovery needed" judgment

**When modifying the `summary` subcommand or `recovery_required` check design**

- [ ] The `CheckRecoveryRequired` call timing and purpose are consistent with section 4
- [ ] Section 4 is updated when check positions are added or modified

---

## 7. Related Documents

- [ADR-0003: ResetForRecovery Phase Design and Handling of Post-Commit Cleanup](../adr/0003_reset_phase_design.md)
- `docs/tasks/0070_entrypoint/02_architecture.md` §3.3 / §6.4
- `docs/tasks/0070_entrypoint/03_implementation_plan.md` Step 1-5 / 3-3
