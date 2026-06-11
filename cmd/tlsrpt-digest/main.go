// Package main is the entry point for the tlsrpt-digest binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/store"
	"github.com/oklog/ulid/v2"
)

var commandRunners = defaultRunners()

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

var (
	errInvalidRecoverMode  = errors.New("invalid recovery mode")
	errUnexpectedArguments = errors.New("unexpected arguments")
	errYesRequiresMode     = errors.New("--yes requires --mode")
	errConfigRequired      = errors.New("--config is required")
	errDryRunNotSupported  = errors.New("--dry-run is not supported for this subcommand")
)

type cliOptions struct {
	ConfigPath      string
	DryRun          bool
	Since           *Duration
	Window          *Duration
	Before          *Duration
	MaxEmailAge     *Duration
	RecoverMode     string
	RecoverYes      bool
	ReprocessNotify bool
}

type cliInvocation struct {
	Subcommand SubcommandName
	Options    cliOptions
	Runner     SubcommandRunner
}

func main() {
	os.Exit(runCLI(context.Background(), os.Args[1:], os.Stderr, BootstrapOptions{}))
}

func runCLI(ctx context.Context, args []string, stderr io.Writer, bootOpts BootstrapOptions) int {
	setupPhase1Logging()

	inv, err := parseCLI(args, stderr)
	if err != nil {
		// recover-specific confirmation errors are non-destructive operator prompts,
		// not usage errors — return exitError (1) not exitUsage (2).
		if errors.Is(err, errYesRequiresMode) {
			return exitError
		}
		return exitUsage
	}

	runID := ulid.Make().String()
	logger := slog.Default().With("run_id", runID)
	logger.Info("tlsrpt-digest starting", "subcommand", inv.Subcommand, "dry_run", inv.Options.DryRun)

	bootOpts.DryRun = inv.Options.DryRun
	bootOpts.Logger = logger
	if inv.Subcommand == subcommandRecover {
		m := recoverStoreOpenMode(inv.Options)
		bootOpts.StoreOpenModeOverride = &m
	}
	boot, err := Bootstrap(inv.Subcommand, inv.Options.ConfigPath, runID, bootOpts)
	if err != nil {
		logger.Error("bootstrap failed", "error", err)
		return exitError
	}
	boot.Options = inv.Options
	defer func() {
		if err := boot.Close(); err != nil {
			logger.Error("failed to close bootstrap resources", "error", err)
		}
	}()

	exitCode, err := inv.Runner.Run(ctx, boot)
	if err != nil {
		logger.Error("subcommand failed", "error", err)
		if exitCode == exitOK {
			return exitError
		}
	}
	return exitCode
}

func parseCLI(args []string, stderr io.Writer) (cliInvocation, error) {
	// Step 1: Parse the global --config flag that must precede the subcommand.
	global := flag.NewFlagSet("tlsrpt-digest", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	var configPath string
	global.StringVar(&configPath, "config", "", "path to TOML configuration file (required)")
	global.StringVar(&configPath, "c", "", "shorthand for --config")

	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDetailedHelp(stderr)
		} else {
			_, _ = fmt.Fprintln(stderr, err)
			printUsage(stderr)
		}
		return cliInvocation{}, err
	}

	remaining := global.Args()

	// Step 2: Require at least a subcommand.
	if len(remaining) == 0 {
		printDetailedHelp(stderr)
		return cliInvocation{}, flag.ErrHelp
	}

	// Step 3: Handle the help subcommand.
	if SubcommandName(remaining[0]) == subcommandHelp {
		printDetailedHelp(stderr)
		return cliInvocation{}, flag.ErrHelp
	}

	// Step 4: Dispatch to the subcommand runner.
	subcmd := SubcommandName(remaining[0])
	runner, ok := commandRunners[subcmd]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q\n", remaining[0])
		printUsage(stderr)
		return cliInvocation{}, flag.ErrHelp
	}

	// Step 5: Parse subcommand-specific flags.
	fs := flag.NewFlagSet(string(subcmd), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := cliOptions{ConfigPath: configPath}
	fs.BoolVar(&opts.DryRun, "dry-run", false, "preview without making changes: skip local store writes and IMAP mailbox modifications, and log what would happen instead of sending Slack notifications (store opened read-only)")
	fs.BoolVar(&opts.DryRun, "n", false, "shorthand for --dry-run")
	registerFlags(fs, subcmd, &opts)

	if err := fs.Parse(remaining[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDetailedHelp(stderr)
		} else {
			_, _ = fmt.Fprintln(stderr, err)
			printUsage(stderr)
		}
		return cliInvocation{}, err
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
		printUsage(stderr)
		return cliInvocation{}, errUnexpectedArguments
	}
	if err := validateFlags(subcmd, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		if errors.Is(err, errConfigRequired) {
			printDetailedHelp(stderr)
		} else {
			printUsage(stderr)
		}
		return cliInvocation{}, err
	}
	return cliInvocation{Subcommand: subcmd, Options: opts, Runner: runner}, nil
}

func registerFlags(fs *flag.FlagSet, subcmd SubcommandName, opts *cliOptions) {
	switch subcmd {
	case subcommandFetch:
		fs.Var(newDurationFlag(&opts.Since), "since", "fetch window duration")
	case subcommandSummary:
		fs.Var(newDurationFlag(&opts.Window), "window", "summary window duration")
	case subcommandGC:
		fs.Var(newDurationFlag(&opts.Before), "before", "report retention duration")
		fs.Var(newDurationFlag(&opts.MaxEmailAge), "max-email-age", "email retention duration")
	case subcommandRecover:
		fs.StringVar(&opts.RecoverMode, "mode", "", "recovery mode")
		fs.BoolVar(&opts.RecoverYes, "yes", false, "confirm recovery action")
	case subcommandReprocess:
		fs.BoolVar(&opts.ReprocessNotify, "notify", false, "send notifications during reprocess")
	}
}

func validateFlags(subcmd SubcommandName, opts cliOptions) error {
	if opts.ConfigPath == "" {
		return errConfigRequired
	}
	if opts.DryRun && subcmd != subcommandFetch && subcmd != subcommandSummary && subcmd != subcommandGC {
		return errDryRunNotSupported
	}
	if subcmd != subcommandRecover {
		return nil
	}
	if opts.RecoverMode != "" && opts.RecoverMode != recoverModeKeepOld && opts.RecoverMode != recoverModeDiscardOld {
		return fmt.Errorf("%w: %s", errInvalidRecoverMode, opts.RecoverMode)
	}
	if opts.RecoverYes && opts.RecoverMode == "" {
		return errYesRequiresMode
	}
	return nil
}

// recoverStoreOpenMode returns OpenRecoverReset for destructive recover operations
// (discard-old --yes) and OpenReadWrite for all others.
func recoverStoreOpenMode(opts cliOptions) store.OpenMode {
	if opts.RecoverMode == recoverModeDiscardOld && opts.RecoverYes {
		return store.OpenRecoverReset
	}
	return store.OpenReadWrite
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: tlsrpt-digest --config <path> <fetch|summary|reprocess|gc|recover> [options]")
	_, _ = fmt.Fprintln(w, "Run 'tlsrpt-digest help' or 'tlsrpt-digest -h' for detailed help.")
}

func printDetailedHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  tlsrpt-digest --config <path> <subcommand> [options]

Global flags:
  -c, --config <path>   Path to TOML configuration file (required)

Subcommands:
  fetch       Fetch TLSRPT reports from IMAP and process them
  summary     Send a periodic summary of accumulated reports
  reprocess   Re-parse stored .eml files and rebuild the report store
  gc          Delete report data, .eml files, and (if imap.retention_days > 0) old IMAP messages
  recover     Inspect and repair store consistency
  help        Show this help

Subcommand options:
  fetch:
    -n, --dry-run         Connect to IMAP without downloading messages or sending notifications
    --since <duration>    Override fetch window (default: fetch_days in config)

  summary:
    -n, --dry-run         Log Slack payload instead of sending it
    --window <duration>   Override summary window (default: window_days in config)

  reprocess:
    --notify              Send Slack notifications for TLS failures and parse errors

  gc:
    -n, --dry-run                 Preview deletions without modifying the local store or IMAP mailbox (read-only store, log Slack payload instead of sending it)
    --before <duration>           Override report retention duration (default: retention_days in config)
    --max-email-age <duration>    Override .eml file retention duration (default: max_email_age_days in config)

  recover:
    --mode <keep-old|discard-old>   Recovery mode
    --yes                           Confirm destructive action (required with --mode discard-old)

Duration format: integer followed by d (days) or w (weeks). Examples: 7d, 2w.
`)
}

func setupPhase1Logging() slog.Handler {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
	return h
}

func defaultRunners() map[SubcommandName]SubcommandRunner {
	return map[SubcommandName]SubcommandRunner{
		subcommandFetch:     newFetchRunner(),
		subcommandSummary:   newSummaryRunner(),
		subcommandReprocess: newReprocessRunner(),
		subcommandGC:        newGCRunner(),
		subcommandRecover:   newRecoverRunner(),
	}
}
