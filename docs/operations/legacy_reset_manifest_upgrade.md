# Operations Runbook: Upgrading Stores with Legacy Reset Manifests

## Target Audience

This runbook is for upgrade operators who are upgrading `tlsrpt-digest` to version 0081 or later (which removes `AbortReset` and finalizes the valid phase set to `{1, 4}`).

## Applicability

This runbook applies when any of the following conditions exist:

- A store has a **phase 2 or phase 3** reset manifest (`.tlsrpt-digest-reset-manifest.json`) remaining (the old version crashed while running `recover --mode discard-old --yes` and stopped after writing a checkpoint phase)
- A store has a **phase 5** reset manifest remaining (the old version crashed while running `recover --abort-reset --yes` and stopped after writing the abort WAL entry)

**Normal cases** (no manifest, phase 1 only, or phase 4 only) can be upgraded directly without any preparation. This runbook is not required.

---

## Background

From task 0081 onward, `validateManifestPhase` accepts only phase values `{1, 4}` as valid. When values 2, 3, or 5 written by older versions are detected, the new version returns `ErrResetManifestPhaseUnknown` and stops (fail-closed). This is a fail-closed safety design to prevent data corruption; it requires the operator to take manual action.

---

## How to Check Before Upgrading

Run the following command before upgrading to check the manifest phase value.

```bash
cat "${STORE_ROOT_DIR}/.tlsrpt-digest-reset-manifest.json"
```

Example output (phase 2):

```json
{"version":1,"curr_uid_validity":12345678,"phase":2}
```

If the `"phase"` value is `2`, `3`, or `5`, this runbook's procedure is required. If `"phase"` is `1` or `4`, or if the file does not exist, no action is needed.

---

## When a Phase 2 or Phase 3 Manifest Remains

### Overview

Phases 2 and 3 are the checkpoint phases that older code wrote (removed in task 0080). These phases mean that `recover --mode discard-old --yes` crashed partway through. In older versions, these were treated as pre-commit states, and re-running the command would converge the state correctly.

### Procedure

Before upgrading, run the following with the **old version** to complete the reset.

```bash
tlsrpt-digest recover --mode discard-old --yes
```

Verify that the command completed successfully (exits with code 0 and the manifest file is deleted).

```bash
ls "${STORE_ROOT_DIR}/.tlsrpt-digest-reset-manifest.json"
# ls: ... No such file or directory  ← normal (manifest deleted)
```

Confirm the manifest is deleted, then proceed with the upgrade.

---

## When a Phase 5 Manifest Remains

### Overview

Phase 5 is the abort WAL entry used by the old version's `AbortReset` (`recover --abort-reset --yes`) (removed in task 0081). This phase means that `AbortReset` crashed while restoring files from staging back to their original locations. In older versions, re-running `AbortReset` converged the state correctly.

### Procedure

Before upgrading, run the following with the **old version** to complete the abort operation.

```bash
tlsrpt-digest recover --abort-reset --yes
```

Verify that the command completed successfully (exits with code 0 and the manifest file is deleted).

```bash
ls "${STORE_ROOT_DIR}/.tlsrpt-digest-reset-manifest.json"
# ls: ... No such file or directory  ← normal (manifest deleted)
```

Confirm the manifest is deleted, then proceed with the upgrade.

---

## Post-Upgrade Verification

### When Upgraded with Legacy Manifests Still Present

If the upgrade is performed with legacy manifests (phase 2, 3, or 5) still present, the new version will return the following error and stop on any write operation such as `fetch`, `gc`, or `recover`.

```
store: unknown reset manifest phase: got=N
```

(where `N` is the detected phase value)

In this state, the following are guaranteed:

- **The staging directory and manifest file are not deleted** (fail-closed design).
- The store's consistency is preserved.

Temporarily roll back to the old version, complete the preparation steps above (`discard-old --yes` or `abort-reset --yes`), and then redo the upgrade.

### Verifying Normal Operation After Upgrade

After upgrading, verify operation with the following command.

```bash
tlsrpt-digest recover
```

If `No recovery required: store is in a consistent state.` or a display showing that recovery is required appears, the upgrade was successful.

---

## Related Documents

- [ADR-0003: ResetForRecovery Phase Design](../dev/adr/0003_reset_phase_design.md)
- [Inter-Process Locking Design Guidelines](../dev/developer_guide/process_locking.md)
