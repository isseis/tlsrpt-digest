//go:build test

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeRecoverBoot creates a minimal BootContext for recover tests.
func makeRecoverBoot(t *testing.T, st *storetestutil.FakeStore, opts cliOptions) *BootContext {
	t.Helper()
	cfg := &config.Config{}
	cfg.IMAP.Host = "imap.example.com"
	cfg.IMAP.Port = 993
	cfg.IMAP.Mailbox = "INBOX"
	cfg.Store.RootDir = "/data/store"
	return &BootContext{
		Config:  cfg,
		Store:   st,
		Options: opts,
	}
}

// makeRecoveryStore returns a FakeStore with recovery-required set.
func makeRecoveryStore(prev, curr uint32) *storetestutil.FakeStore {
	st := storetestutil.NewFakeStore()
	st.Recovery = &storetestutil.FakeRecovery{Prev: prev, Curr: curr, DetectedAt: time.Now()}
	return st
}

// TestRecover_ModeFlag verifies that the --mode flag is registered and accepts keep-old/discard-old.
func TestRecover_ModeFlag(t *testing.T) {
	for _, mode := range []string{"keep-old", "discard-old"} {
		inv, err := parseCLI([]string{"--config", "custom.toml", "recover", "--mode", mode}, io.Discard)
		require.NoError(t, err, "mode %s should be accepted", mode)
		assert.Equal(t, mode, inv.Options.RecoverMode)
	}
}

// TestRecover_InvalidMode verifies that an invalid mode value is rejected at parse time.
func TestRecover_InvalidMode(t *testing.T) {
	_, err := parseCLI([]string{"recover", "--mode", "nope"}, io.Discard)
	assert.Error(t, err)
}

// TestRecover_KeepOldCallsApplyRecovery verifies keep-old calls ApplyRecovery and
// displays previous/current UIDVALIDITY, mailbox, mode, and the old-epoch warning.
func TestRecover_KeepOldCallsApplyRecovery(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "keep-old"}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, st.ApplyRecoveryCallCount)
	assert.Nil(t, st.Recovery, "recovery-required should be cleared after apply")
	output := out.String()
	assert.Contains(t, output, "100") // previous UIDVALIDITY
	assert.Contains(t, output, "200") // current UIDVALIDITY
	assert.Contains(t, output, "imap.example.com:993/INBOX")
	assert.Contains(t, output, "keep-old")
	assert.Contains(t, output, "Warning")
	assert.Contains(t, output, "previous UIDVALIDITY epoch")
}

// TestRecover_ApplyRecoveryFailure verifies that an ApplyRecovery error leaves
// recovery-required intact and returns exit 1.
func TestRecover_ApplyRecoveryFailure(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	st.ApplyRecoveryErr = errors.New("disk full")
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "keep-old"}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.NotNil(t, st.Recovery, "recovery-required should be preserved on failure")
}

// TestRecover_DiscardOldYesCallsResetForRecovery verifies that discard-old --yes
// calls ResetForRecovery and displays the planned actions.
func TestRecover_DiscardOldYesCallsResetForRecovery(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	st.PendingReset = true
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, st.ResetForRecoveryCallCount)
	assert.Nil(t, st.Recovery, "recovery-required should be cleared after reset")
	assert.False(t, st.PendingReset, "pending reset flag should be cleared after reset")
	output := out.String()
	assert.Contains(t, output, "200") // current UIDVALIDITY
	assert.Contains(t, output, "discard-old --yes")
}

// TestRecover_DiscardOldYesFreshStart verifies that discard-old --yes succeeds on a
// first-time reset where no prior manifest exists (PendingReset = false).
func TestRecover_DiscardOldYesFreshStart(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	// PendingReset is false — no prior incomplete reset manifest.
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, st.ResetForRecoveryCallCount)
	assert.Nil(t, st.Recovery, "recovery-required should be cleared after reset")
	output := out.String()
	assert.NotContains(t, output, "Continuing incomplete reset", "fresh start should not show resume message")
	assert.Contains(t, output, "Recovery completed")
}

// TestRecover_ResetForRecoveryFailure verifies that a ResetForRecovery error leaves
// recovery-required and pending-reset state intact and returns exit 1.
func TestRecover_ResetForRecoveryFailure(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	st.PendingReset = true
	st.ResetForRecoveryErr = errors.New("write error")
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.NotNil(t, st.Recovery, "recovery-required should be preserved on failure")
	assert.True(t, st.PendingReset, "pending reset flag should be preserved on failure")
}

// TestRecover_DiscardOldDryRun verifies that discard-old without --yes does not make
// destructive changes, displays the planned actions (including initialized_at preservation),
// and returns exit 1.
func TestRecover_DiscardOldDryRun(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old"}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, st.ResetForRecoveryCallCount)
	assert.NotNil(t, st.Recovery, "recovery-required should not be modified")
	output := out.String()
	assert.Contains(t, output, "Report store will be replaced with an empty set")
	assert.Contains(t, output, ".eml store will be replaced with an empty state")
	assert.Contains(t, output, "uid_validity")
	assert.Contains(t, output, "initialized_at")
	assert.Contains(t, output, "No changes made")
}

// TestRecover_YesAlone verifies that --yes without --mode is rejected at parse time
// with a descriptive error message.
func TestRecover_YesAlone(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseCLI([]string{"--config", "test.toml", "recover", "--yes"}, &stderr)
	assert.Error(t, err)
	assert.Contains(t, stderr.String(), "--yes requires --mode")
	assert.NotContains(t, stderr.String(), "--abort-reset")
}

// TestRecover_NoRecoveryRequired verifies that all modes exit 1 with an explanation when
// no recovery-required state exists and the store is clean, without making any store changes.
func TestRecover_NoRecoveryRequired(t *testing.T) {
	st := storetestutil.NewFakeStore() // no Recovery set, PendingReset=false
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	for _, opts := range []cliOptions{
		{},
		{RecoverMode: "keep-old"},
		{RecoverMode: "discard-old"},
		{RecoverMode: "discard-old", RecoverYes: true},
	} {
		st.ApplyRecoveryCallCount = 0
		st.ResetForRecoveryCallCount = 0
		out.Reset()

		code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))
		require.NoError(t, err)
		assert.Equal(t, exitError, code, "store is consistent: exit 1 expected for opts %+v", opts)
		assert.Equal(t, 0, st.ApplyRecoveryCallCount)
		assert.Equal(t, 0, st.ResetForRecoveryCallCount)
		assert.Contains(t, out.String(), "No recovery required")
	}
}

// TestRecover_CommittedCleanupPending_StatusDisplay verifies that when recovery_required is absent
// but a pending-reset manifest exists (crash between commitReset sentinel write and final cleanup),
// recover without --yes informs the operator and exits 1 without making store changes.
func TestRecover_CommittedCleanupPending_StatusDisplay(t *testing.T) {
	for _, opts := range []cliOptions{
		{},
		{RecoverMode: "keep-old"},
		{RecoverMode: "discard-old"},
	} {
		st := storetestutil.NewFakeStore() // no Recovery (found=false)
		st.PendingReset = true
		var out bytes.Buffer
		runner := &recoverRunner{stdout: &out}

		code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))
		require.NoError(t, err)
		assert.Equal(t, exitError, code)
		assert.Equal(t, 0, st.ResetForRecoveryCallCount)
		assert.Equal(t, 0, st.ApplyRecoveryCallCount)
		output := out.String()
		assert.Contains(t, output, "Previous reset committed")
		assert.Contains(t, output, "discard-old --yes")
	}
}

// TestRecover_CommittedCleanupPending_DiscardOldYes verifies that --mode discard-old --yes
// calls ResetForRecovery to finalize cleanup when recovery_required is absent but a
// pending-reset manifest is still present.
func TestRecover_CommittedCleanupPending_DiscardOldYes(t *testing.T) {
	st := storetestutil.NewFakeStore() // no Recovery (found=false)
	st.PendingReset = true
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, st.ResetForRecoveryCallCount)
	assert.Contains(t, out.String(), "Recovery completed")
}

// TestRecover_CommittedCleanupPending_ResetForRecoveryFailure verifies that a
// ResetForRecovery error in the cleanup-pending path returns exit 1 with an error.
func TestRecover_CommittedCleanupPending_ResetForRecoveryFailure(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.PendingReset = true
	st.ResetForRecoveryErr = errors.New("disk full")
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	assert.Error(t, err)
	assert.Equal(t, exitError, code)
}

// TestRecover_StatusDisplayNoMode verifies that plain recover (no mode) with recovery required
// displays UIDVALIDITY/mailbox/path/mode and exits 1 without making changes.
func TestRecover_StatusDisplayNoMode(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, cliOptions{}))

	require.NoError(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, st.ApplyRecoveryCallCount)
	assert.Equal(t, 0, st.ResetForRecoveryCallCount)
	assert.NotNil(t, st.Recovery)
	output := out.String()
	assert.Contains(t, output, "100") // previous UIDVALIDITY
	assert.Contains(t, output, "200") // current UIDVALIDITY
	assert.Contains(t, output, "status display")
}

// TestRecover_PendingResetDisplaysOptions verifies that when a pending reset is present
// in the store, the available recovery options are shown in stdout.
func TestRecover_PendingResetDisplaysOptions(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	st.PendingReset = true
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	output := out.String()
	assert.Contains(t, output, "Pending reset: detected")
	assert.Contains(t, output, "discard-old --yes")
	assert.NotContains(t, output, "abort-reset")
}

// TestRecover_NoPendingResetShowsNone verifies that when no pending reset exists,
// the display shows "Pending reset: none" and no options are listed.
func TestRecover_NoPendingResetShowsNone(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	// PendingReset defaults to false
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "keep-old"}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	output := out.String()
	assert.Contains(t, output, "Pending reset: none")
	assert.NotContains(t, output, "Available options")
}

// TestBootstrap_PendingResetShowsGuidance verifies that when OpenReadWrite fails with
// ErrPendingReset (e.g. during fetch or gc), the Bootstrap error includes guidance for
// the continue path.
func TestBootstrap_PendingResetShowsGuidance(t *testing.T) {
	_, err := Bootstrap(subcommandFetch, "config.toml", "run-id", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) {
			return configForRoot(secureStoreRoot(t)), nil
		},
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			return &SpyNotificationSink{}, nil
		},
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return nil, store.ErrPendingReset
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recover --mode discard-old --yes")
	assert.NotContains(t, err.Error(), "abort-reset")
}

// TestRecover_PendingResetShowsStatusForNonDestructiveModes verifies that recover opens
// the store with OpenReadWrite for non-destructive modes, and that even when the FakeStore
// reports a pending reset the runner shows status and does not call destructive operations.
func TestRecover_PendingResetShowsStatusForNonDestructiveModes(t *testing.T) {
	tests := []struct {
		name string
		opts cliOptions
	}{
		{name: "plain recover", opts: cliOptions{}},
		{name: "keep old", opts: cliOptions{RecoverMode: "keep-old"}},
		{name: "discard old dry run", opts: cliOptions{RecoverMode: "discard-old"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := makeRecoveryStore(100, 200)
			st.PendingReset = true

			boot, err := Bootstrap(subcommandRecover, "config.toml", "run-recover-pending", BootstrapOptions{
				LoadConfig: func(string) (*config.Config, error) {
					return configForRoot(secureStoreRoot(t)), nil
				},
				BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
					return &SpyNotificationSink{}, nil
				},
				OpenStore: func(_ string, _ store.IMAPIdentity, mode store.OpenMode) (store.Store, error) {
					assert.Equal(t, store.OpenReadWrite, mode)
					return st, nil
				},
			})
			require.NoError(t, err)
			defer func() { require.NoError(t, boot.Close()) }()
			boot.Options = tt.opts

			var out bytes.Buffer
			runner := &recoverRunner{stdout: &out}
			code, err := runner.Run(context.Background(), boot)

			require.NoError(t, err)
			assert.Equal(t, exitError, code)
			assert.Equal(t, 0, st.ApplyRecoveryCallCount)
			assert.Equal(t, 0, st.ResetForRecoveryCallCount)
			assert.NotNil(t, st.Recovery)
			output := out.String()
			assert.Contains(t, output, "Recovery required for mailbox: imap.example.com:993/INBOX")
			assert.Contains(t, output, "Previous UIDVALIDITY: 100")
			assert.Contains(t, output, "Current UIDVALIDITY:  200")
			assert.Contains(t, output, "Local data path:")
			assert.Contains(t, output, "Pending reset: detected")
			assert.Contains(t, output, "recover --mode discard-old --yes")
			assert.NotContains(t, output, "abort-reset")
		})
	}
}

// TestRecover_HasPendingResetFailure verifies that status inspection errors are surfaced
// without mutating recovery state.
func TestRecover_HasPendingResetFailure(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	st.HasPendingResetErr = errors.New("manifest unreadable")
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, cliOptions{}))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "check pending reset")
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, st.ApplyRecoveryCallCount)
	assert.Equal(t, 0, st.ResetForRecoveryCallCount)
	assert.NotNil(t, st.Recovery)
}

// TestRecover_LoadRecoveryRequiredFailure verifies that a LoadRecoveryRequired error
// returns exit 1 with an error.
func TestRecover_LoadRecoveryRequiredFailure(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.LoadRecoveryRequiredErr = errors.New("disk error")
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, cliOptions{}))

	assert.Error(t, err)
	assert.Equal(t, exitError, code)
}

// TestRecover_DiscardOldYesDisplaysActions verifies that discard-old --yes shows all
// planned changes including initialized_at and mailbox identity preservation.
func TestRecover_DiscardOldYesDisplaysActions(t *testing.T) {
	st := makeRecoveryStore(100, 200)
	st.PendingReset = true
	var out bytes.Buffer
	runner := &recoverRunner{stdout: &out}

	opts := cliOptions{RecoverMode: "discard-old", RecoverYes: true}
	code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, opts))

	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	output := out.String()
	assert.Contains(t, output, "Report store will be replaced with an empty set")
	assert.Contains(t, output, ".eml store will be replaced with an empty state")
	assert.Contains(t, output, "uid_validity")
	assert.Contains(t, output, "initialized_at")
	assert.Contains(t, output, "200") // current UIDVALIDITY
}

// TestRecover_ExitCodes verifies expected exit codes for keep-old success, dry-run, and
// no-recovery-required cases.
func TestRecover_ExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		opts     cliOptions
		recovery bool
		wantCode int
		wantErr  bool
	}{
		{
			name:     "no recovery required exits 1",
			opts:     cliOptions{RecoverMode: "keep-old"},
			recovery: false,
			wantCode: exitError,
			wantErr:  false,
		},
		{
			name:     "keep-old success exits 0",
			opts:     cliOptions{RecoverMode: "keep-old"},
			recovery: true,
			wantCode: exitOK,
			wantErr:  false,
		},
		{
			name:     "discard-old dry-run exits 1",
			opts:     cliOptions{RecoverMode: "discard-old"},
			recovery: true,
			wantCode: exitError,
			wantErr:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var st *storetestutil.FakeStore
			if tc.recovery {
				st = makeRecoveryStore(1, 2)
			} else {
				st = storetestutil.NewFakeStore()
			}
			var out bytes.Buffer
			runner := &recoverRunner{stdout: &out}
			code, err := runner.Run(context.Background(), makeRecoverBoot(t, st, tc.opts))
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.wantCode, code)
		})
	}
}
