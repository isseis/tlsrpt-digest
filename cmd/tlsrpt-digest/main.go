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

	"github.com/oklog/ulid/v2"
)

const defaultConfigPath = "./config.toml"

var commandRunners = defaultRunners()

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

var (
	errInvalidRecoverMode  = errors.New("invalid recovery mode")
	errUnexpectedArguments = errors.New("unexpected arguments")
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
	RecoverAbort    bool
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
		return exitUsage
	}

	runID := ulid.Make().String()
	logger := slog.Default().With("run_id", runID)
	logger.Info("tlsrpt-digest starting", "subcommand", inv.Subcommand, "dry_run", inv.Options.DryRun)

	bootOpts.DryRun = inv.Options.DryRun
	bootOpts.RecoverResetMode = inv.Options.RecoverYes && (inv.Options.RecoverMode == "discard-old" || inv.Options.RecoverAbort)
	bootOpts.Logger = logger
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
	if len(args) == 0 {
		printUsage(stderr)
		return cliInvocation{}, flag.ErrHelp
	}
	subcmd := SubcommandName(args[0])
	runner, ok := commandRunners[subcmd]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q\n", args[0])
		printUsage(stderr)
		return cliInvocation{}, flag.ErrHelp
	}

	fs := flag.NewFlagSet(string(subcmd), flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := cliOptions{ConfigPath: defaultConfigPath}
	fs.StringVar(&opts.ConfigPath, "config", defaultConfigPath, "path to TOML configuration file")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "log notification payloads to stderr without sending HTTP requests")
	registerFlags(fs, subcmd, &opts)

	if err := fs.Parse(args[1:]); err != nil {
		printUsage(stderr)
		return cliInvocation{}, err
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
		printUsage(stderr)
		return cliInvocation{}, errUnexpectedArguments
	}
	if err := validateFlags(subcmd, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		printUsage(stderr)
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
		fs.BoolVar(&opts.RecoverAbort, "abort-reset", false, "abort pending reset")
	case subcommandReprocess:
		fs.BoolVar(&opts.ReprocessNotify, "notify", false, "send notifications during reprocess")
	}
}

func validateFlags(subcmd SubcommandName, opts cliOptions) error {
	if subcmd == subcommandRecover && opts.RecoverMode != "" {
		if opts.RecoverMode != recoverModeKeepOld && opts.RecoverMode != recoverModeDiscardOld {
			return fmt.Errorf("%w: %s", errInvalidRecoverMode, opts.RecoverMode)
		}
	}
	return nil
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: tlsrpt-digest <fetch|summary|reprocess|gc|recover> [options]")
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
