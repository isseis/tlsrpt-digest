package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/store"
)

const recoverModeDiscardOld = "discard-old"

// recoverRunner implements SubcommandRunner for the recover subcommand.
type recoverRunner struct {
	stdout io.Writer
}

func newRecoverRunner() *recoverRunner {
	return &recoverRunner{stdout: os.Stdout}
}

// Run executes the recover subcommand: display recovery state and optionally apply recovery.
func (r *recoverRunner) Run(_ context.Context, boot *BootContext) (int, error) {
	opts := boot.Options

	if code, done := r.validateFlags(opts); done {
		return code, nil
	}

	prev, curr, _, found, err := boot.Store.LoadRecoveryRequired()
	if err != nil {
		return exitError, fmt.Errorf("recover: load recovery-required: %w", err)
	}
	if !found {
		_, _ = fmt.Fprintln(r.stdout, "No recovery required: store is in a consistent state.")
		return exitError, nil
	}

	pendingReset, err := boot.Store.HasPendingReset()
	if err != nil {
		return exitError, fmt.Errorf("recover: check pending reset: %w", err)
	}

	r.printInfo(boot, prev, curr, opts, pendingReset)

	return r.executeMode(boot.Store, opts, curr, pendingReset)
}

// validateFlags checks flag combinations. Returns (exitCode, done=true) if validation fails.
func (r *recoverRunner) validateFlags(opts cliOptions) (int, bool) {
	if opts.RecoverAbort && !opts.RecoverYes {
		_, _ = fmt.Fprintln(r.stdout, "error: --abort-reset requires --yes to confirm")
		return exitError, true
	}
	if opts.RecoverYes && !opts.RecoverAbort && opts.RecoverMode == "" {
		_, _ = fmt.Fprintln(r.stdout, "error: --yes requires --mode or --abort-reset")
		return exitError, true
	}
	return 0, false
}

// printInfo writes the operator summary to stdout.
func (r *recoverRunner) printInfo(boot *BootContext, prev, curr uint32, opts cliOptions, pendingReset bool) {
	cfg := boot.Config
	mailbox := fmt.Sprintf("%s:%d/%s", cfg.IMAP.Host, cfg.IMAP.Port, cfg.IMAP.Mailbox)
	pendingResetStatus := "none"
	if pendingReset {
		pendingResetStatus = "detected"
	}
	selectedMode := opts.RecoverMode
	if opts.RecoverAbort {
		selectedMode = "abort-reset"
	}
	if selectedMode == "" {
		selectedMode = "(status display)"
	}
	_, _ = fmt.Fprintf(r.stdout, "Recovery required for mailbox: %s\n", mailbox)
	_, _ = fmt.Fprintf(r.stdout, "  Previous UIDVALIDITY: %d\n", prev)
	_, _ = fmt.Fprintf(r.stdout, "  Current UIDVALIDITY:  %d\n", curr)
	_, _ = fmt.Fprintf(r.stdout, "  Local data path: %s\n", cfg.Store.RootDir)
	_, _ = fmt.Fprintf(r.stdout, "  Selected mode: %s\n", selectedMode)
	_, _ = fmt.Fprintf(r.stdout, "  Pending reset: %s\n", pendingResetStatus)
	if pendingReset {
		_, _ = fmt.Fprintln(r.stdout, "")
		_, _ = fmt.Fprintln(r.stdout, "A pending reset was detected. Available options:")
		_, _ = fmt.Fprintln(r.stdout, "  Continue reset:  tlsrpt-digest recover --mode discard-old --yes")
		_, _ = fmt.Fprintln(r.stdout, "  Roll back reset: tlsrpt-digest recover --abort-reset --yes")
	}
}

// executeMode dispatches to the appropriate recovery action.
func (r *recoverRunner) executeMode(st store.Store, opts cliOptions, curr uint32, pendingReset bool) (int, error) {
	switch {
	case opts.RecoverAbort:
		return r.runAbortReset(st)
	case opts.RecoverMode == "keep-old":
		return r.runKeepOld(st, curr)
	case opts.RecoverMode == recoverModeDiscardOld:
		return r.runDiscardOld(st, curr, opts.RecoverYes, pendingReset)
	default:
		return exitError, nil
	}
}

func (r *recoverRunner) runAbortReset(st store.Store) (int, error) {
	if err := st.AbortReset(); err != nil {
		if errors.Is(err, store.ErrResetNotPending) {
			_, _ = fmt.Fprintln(r.stdout, "error: no pending reset to abort")
		} else {
			_, _ = fmt.Fprintln(r.stdout, "error: abort reset failed")
		}
		return exitError, fmt.Errorf("recover: abort reset: %w", err)
	}
	_, _ = fmt.Fprintln(r.stdout, "Pending reset aborted. Recovery-required state preserved.")
	return exitOK, nil
}

func (r *recoverRunner) runKeepOld(st store.Store, curr uint32) (int, error) {
	_, _ = fmt.Fprintln(r.stdout, "")
	_, _ = fmt.Fprintln(r.stdout, "Warning: existing reports and .eml files from the previous UIDVALIDITY epoch will be retained.")
	_, _ = fmt.Fprintln(r.stdout, "These may appear in future summary results if they fall within the configured time window.")
	if err := st.ApplyRecovery(curr); err != nil {
		return exitError, fmt.Errorf("recover: apply recovery: %w", err)
	}
	_, _ = fmt.Fprintln(r.stdout, "Recovery applied. Store is now consistent with current UIDVALIDITY.")
	return exitOK, nil
}

func (r *recoverRunner) runDiscardOld(st store.Store, curr uint32, confirmed bool, pendingReset bool) (int, error) {
	_, _ = fmt.Fprintln(r.stdout, "")
	_, _ = fmt.Fprintln(r.stdout, "The following changes will be made:")
	_, _ = fmt.Fprintln(r.stdout, "  - Report store will be replaced with an empty set.")
	_, _ = fmt.Fprintln(r.stdout, "  - .eml store will be replaced with an empty state.")
	_, _ = fmt.Fprintf(r.stdout, "  - Sentinel uid_validity will be updated to current value: %d\n", curr)
	_, _ = fmt.Fprintln(r.stdout, "  - Sentinel initialized_at and mailbox identity (host/port/mailbox) are preserved.")
	if pendingReset {
		_, _ = fmt.Fprintln(r.stdout, "  (Continuing incomplete reset from a previous run.)")
	}
	if !confirmed {
		_, _ = fmt.Fprintln(r.stdout, "No changes made. Re-run with --yes to apply.")
		return exitError, nil
	}
	if err := st.ResetForRecovery(curr); err != nil {
		return exitError, fmt.Errorf("recover: reset for recovery: %w", err)
	}
	_, _ = fmt.Fprintln(r.stdout, "Recovery completed. Store reset to empty state with current UIDVALIDITY.")
	return exitOK, nil
}
