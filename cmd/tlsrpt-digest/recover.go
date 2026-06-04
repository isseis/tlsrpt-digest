package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/store"
)

const (
	recoverModeKeepOld    = "keep-old"
	recoverModeDiscardOld = "discard-old"
)

type recoverRunner struct {
	stdout io.Writer
}

func newRecoverRunner() *recoverRunner {
	return &recoverRunner{stdout: os.Stdout}
}

// Run executes the recover subcommand: display recovery state and optionally apply recovery.
func (r *recoverRunner) Run(_ context.Context, boot *BootContext) (int, error) {
	opts := boot.Options

	prev, curr, _, found, err := boot.Store.LoadRecoveryRequired()
	if err != nil {
		return exitError, fmt.Errorf("recover: load recovery-required: %w", err)
	}
	if !found {
		return r.handleNoRecoveryRequired(boot.Store, opts)
	}

	pendingReset, err := boot.Store.HasPendingReset()
	if err != nil {
		return exitError, fmt.Errorf("recover: check pending reset: %w", err)
	}

	r.printInfo(boot, prev, curr, opts, pendingReset)

	return r.executeMode(boot.Store, opts, curr, pendingReset)
}

func (r *recoverRunner) printInfo(boot *BootContext, prev, curr uint32, opts cliOptions, pendingReset bool) {
	cfg := boot.Config
	mailbox := fmt.Sprintf("%s:%d/%s", cfg.IMAP.Host, cfg.IMAP.Port, cfg.IMAP.Mailbox)
	pendingResetStatus := "none"
	if pendingReset {
		pendingResetStatus = "detected"
	}
	selectedMode := opts.RecoverMode
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
		_, _ = fmt.Fprintln(r.stdout, "  Continue reset:  tlsrpt-digest --config <path> recover --mode discard-old --yes")
	}
}

func (r *recoverRunner) executeMode(st store.Store, opts cliOptions, curr uint32, pendingReset bool) (int, error) {
	switch {
	case pendingReset && opts.RecoverMode == recoverModeKeepOld:
		_, _ = fmt.Fprintln(r.stdout, "")
		_, _ = fmt.Fprintln(r.stdout, "No changes made. Resolve the pending reset before applying keep-old recovery.")
		return exitError, nil
	case opts.RecoverMode == recoverModeKeepOld:
		return r.runKeepOld(st, curr)
	case opts.RecoverMode == recoverModeDiscardOld:
		return r.runDiscardOld(st, curr, opts.RecoverYes, pendingReset)
	default:
		return exitError, nil
	}
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
	if !confirmed {
		if pendingReset {
			_, _ = fmt.Fprintln(r.stdout, "  (A previous incomplete reset will be resumed when re-run with --yes.)")
		}
		_, _ = fmt.Fprintln(r.stdout, "No changes made. Re-run with --yes to apply.")
		return exitError, nil
	}
	if pendingReset {
		_, _ = fmt.Fprintln(r.stdout, "  (Continuing incomplete reset from a previous run.)")
	}
	if err := st.ResetForRecovery(curr); err != nil {
		return exitError, fmt.Errorf("recover: reset for recovery: %w", err)
	}
	_, _ = fmt.Fprintln(r.stdout, "Recovery completed. Store reset to empty state with current UIDVALIDITY.")
	return exitOK, nil
}

// handleNoRecoveryRequired is called when LoadRecoveryRequired returns found=false.
// A manifest may still exist if a prior discard-old reset committed but its cleanup
// was interrupted; the store is already consistent and only leftover files remain.
// See ADR-0003 §5 for the post-commit cleanup scenario.
func (r *recoverRunner) handleNoRecoveryRequired(st store.Store, opts cliOptions) (int, error) {
	pendingReset, err := st.HasPendingReset()
	if err != nil {
		return exitError, fmt.Errorf("recover: check pending reset: %w", err)
	}
	if !pendingReset {
		_, _ = fmt.Fprintln(r.stdout, "No recovery required: store is in a consistent state.")
		return exitError, nil
	}
	// Post-commit cleanup was interrupted: store is correct but manifest/staging remain.
	if opts.RecoverMode == recoverModeDiscardOld && opts.RecoverYes {
		// Pass 0: sentinel already committed, so ResetForRecovery resumes from the
		// manifest's stored CurrUIDValidity rather than initiating a fresh reset.
		if err := st.ResetForRecovery(0); err != nil {
			return exitError, fmt.Errorf("recover: finalize pending cleanup: %w", err)
		}
		_, _ = fmt.Fprintln(r.stdout, "Recovery completed: previous reset cleanup finalized.")
		return exitOK, nil
	}
	_, _ = fmt.Fprintln(r.stdout, "Previous reset committed: pending cleanup detected.")
	_, _ = fmt.Fprintln(r.stdout, "The store is already in a consistent state; leftover files will be")
	_, _ = fmt.Fprintln(r.stdout, "removed automatically on the next fetch or gc.")
	_, _ = fmt.Fprintln(r.stdout, "Or finalize now: tlsrpt-digest --config <path> recover --mode discard-old --yes")
	return exitError, nil
}
