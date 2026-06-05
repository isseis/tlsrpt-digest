//go:build test

package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
)

func TestRunCLI_DispatchesSubcommands(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandFetch, subcommandSummary, subcommandReprocess, subcommandGC, subcommandRecover} {
		t.Run(string(subcmd), func(t *testing.T) {
			called := false
			withCommandRunners(t, map[SubcommandName]SubcommandRunner{
				subcmd: runnerFunc(func(_ context.Context, boot *BootContext) (int, error) {
					called = true
					assert.Equal(t, subcmd, boot.Subcommand)
					return 0, nil
				}),
			})

			exitCode := runCLI(context.Background(), []string{"--config", "custom.toml", string(subcmd)}, io.Discard, BootstrapOptions{
				LoadConfig: func(path string) (*config.Config, error) {
					assert.Equal(t, "custom.toml", path)
					return configForRoot(secureStoreRoot(t)), nil
				},
				BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
					return &SpyNotificationSink{}, nil
				},
				OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
					return storetestutil.NewFakeStore(), nil
				},
			})

			assert.Equal(t, 0, exitCode)
			assert.True(t, called)
		})
	}
}

func TestRunCLI_PassesParsedOptionsAndDryRun(t *testing.T) {
	withCommandRunners(t, map[SubcommandName]SubcommandRunner{
		subcommandFetch: runnerFunc(func(_ context.Context, boot *BootContext) (int, error) {
			assert.True(t, boot.Options.DryRun)
			assert.Equal(t, &Duration{Days: 7}, boot.Options.Since)
			return 0, nil
		}),
	})
	gotDryRun := false

	exitCode := runCLI(context.Background(), []string{"--config", "custom.toml", "fetch", "-dry-run", "-since", "7d"}, io.Discard, BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) {
			return configForRoot(secureStoreRoot(t)), nil
		},
		BuildNotifier: func(_ config.Secret, _ config.Secret, _ *config.Config, _ string, dryRun bool) (NotificationSink, error) {
			gotDryRun = dryRun
			return &SpyNotificationSink{}, nil
		},
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return storetestutil.NewFakeStore(), nil
		},
	})

	assert.Equal(t, 0, exitCode)
	assert.True(t, gotDryRun)
}

func TestRunCLI_RecoverResetOpenMode(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantMode store.OpenMode
	}{
		{
			name:     "discard old confirmed",
			args:     []string{"--config", "custom.toml", "recover", "-mode", "discard-old", "-yes"},
			wantMode: store.OpenRecoverReset,
		},
		{
			name:     "keep old confirmed",
			args:     []string{"--config", "custom.toml", "recover", "-mode", "keep-old", "-yes"},
			wantMode: store.OpenReadWrite,
		},
		{
			name:     "discard old unconfirmed",
			args:     []string{"--config", "custom.toml", "recover", "-mode", "discard-old"},
			wantMode: store.OpenReadWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withCommandRunners(t, map[SubcommandName]SubcommandRunner{
				subcommandRecover: runnerFunc(func(context.Context, *BootContext) (int, error) {
					return 0, nil
				}),
			})
			gotMode := store.OpenMode(-1)

			exitCode := runCLI(context.Background(), tt.args, io.Discard, BootstrapOptions{
				LoadConfig: func(string) (*config.Config, error) {
					return configForRoot(secureStoreRoot(t)), nil
				},
				BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
					return &SpyNotificationSink{}, nil
				},
				OpenStore: func(_ string, _ store.IMAPIdentity, mode store.OpenMode) (store.Store, error) {
					gotMode = mode
					return storetestutil.NewFakeStore(), nil
				},
			})

			assert.Equal(t, 0, exitCode)
			assert.Equal(t, tt.wantMode, gotMode)
		})
	}
}

// TestRunCLI_AbortResetFlagUndefined verifies that recover --abort-reset --yes is rejected
// by flag.Parse with exit code 2, because the --abort-reset flag is no longer defined.
func TestRunCLI_AbortResetFlagUndefined(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := runCLI(context.Background(), []string{"recover", "-abort-reset", "-yes"}, &stderr, BootstrapOptions{})
	assert.Equal(t, 2, exitCode)
	assert.Contains(t, stderr.String(), "flag provided but not defined")
}

func TestRunCLI_UsageErrorsExit2(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing subcommand", args: nil},
		{name: "unknown subcommand", args: []string{"bogus"}},
		{name: "bad flag", args: []string{"fetch", "--bad"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode := runCLI(context.Background(), tt.args, io.Discard, BootstrapOptions{})
			assert.Equal(t, 2, exitCode)
		})
	}
}

func TestParseCLI_DryRunSupportedSubcommands(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandFetch, subcommandSummary} {
		t.Run(string(subcmd), func(t *testing.T) {
			inv, err := parseCLI([]string{"--config", "c.toml", string(subcmd), "--dry-run"}, io.Discard)
			require.NoError(t, err)
			assert.True(t, inv.Options.DryRun)
		})
	}
}

func TestParseCLI_DryRunUnsupportedSubcommands(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandGC, subcommandReprocess, subcommandRecover} {
		t.Run(string(subcmd), func(t *testing.T) {
			_, err := parseCLI([]string{"--config", "c.toml", string(subcmd), "--dry-run"}, io.Discard)
			require.ErrorIs(t, err, errDryRunNotSupported)
		})
	}
}

func TestRunCLI_DryRunUnsupportedSubcommandExits2(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandGC, subcommandReprocess, subcommandRecover} {
		t.Run(string(subcmd), func(t *testing.T) {
			exitCode := runCLI(context.Background(), []string{"--config", "c.toml", string(subcmd), "--dry-run"}, io.Discard, BootstrapOptions{})
			assert.Equal(t, exitUsage, exitCode)
		})
	}
}

func TestRunCLI_BootstrapFailureExits1(t *testing.T) {
	exitCode := runCLI(context.Background(), []string{"--config", "missing.toml", "fetch"}, io.Discard, BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) {
			return nil, config.ErrConfigFileRead
		},
	})

	assert.Equal(t, 1, exitCode)
}

func TestParseCLI_ConfigFlagAllSubcommands(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandFetch, subcommandSummary, subcommandReprocess, subcommandGC, subcommandRecover} {
		t.Run(string(subcmd), func(t *testing.T) {
			inv, err := parseCLI([]string{"--config", "custom.toml", string(subcmd)}, io.Discard)
			require.NoError(t, err)
			assert.Equal(t, "custom.toml", inv.Options.ConfigPath)
		})
	}
}

func TestParseCLI_MissingConfigReturnsError(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandFetch, subcommandSummary, subcommandReprocess, subcommandGC, subcommandRecover} {
		t.Run(string(subcmd), func(t *testing.T) {
			_, err := parseCLI([]string{string(subcmd)}, io.Discard)
			require.Error(t, err)
			assert.ErrorIs(t, err, errConfigRequired)
		})
	}
}

func TestRunCLI_HelpExits2(t *testing.T) {
	for _, args := range [][]string{
		{"help"},
		{"-h"},
		{"--help"},
	} {
		t.Run(args[0], func(t *testing.T) {
			var buf bytes.Buffer
			exitCode := runCLI(context.Background(), args, &buf, BootstrapOptions{})
			assert.Equal(t, 2, exitCode)
			assert.Contains(t, buf.String(), "Usage:")
		})
	}
}

func TestParseCLI_ShortFlags(t *testing.T) {
	inv, err := parseCLI([]string{"-c", "custom.toml", "fetch", "-n"}, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, "custom.toml", inv.Options.ConfigPath)
	assert.True(t, inv.Options.DryRun)
}

func TestParseCLI_InvalidDurationFlag(t *testing.T) {
	_, err := parseCLI([]string{"fetch", "--since", "0d"}, io.Discard)
	require.Error(t, err)
}

type runnerFunc func(context.Context, *BootContext) (int, error)

func (f runnerFunc) Run(ctx context.Context, boot *BootContext) (int, error) {
	return f(ctx, boot)
}

func withCommandRunners(t *testing.T, runners map[SubcommandName]SubcommandRunner) {
	t.Helper()
	orig := commandRunners
	commandRunners = runners
	t.Cleanup(func() {
		commandRunners = orig
	})
}
