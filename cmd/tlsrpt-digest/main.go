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

var errInvalidRecoverMode = errors.New("invalid recovery mode")

type cliOptions struct {
	ConfigPath      string
	DryRun          bool
	Since           string
	Window          string
	Before          string
	MaxEmailAge     string
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
	slog.Info("tlsrpt-digest starting", "run_id", runID, "subcommand", inv.Subcommand, "dry_run", inv.Options.DryRun)

	bootOpts.DryRun = inv.Options.DryRun
	bootOpts.RecoverResetMode = inv.Options.RecoverYes && (inv.Options.RecoverMode == "discard-old" || inv.Options.RecoverAbort)
	boot, err := Bootstrap(inv.Subcommand, inv.Options.ConfigPath, runID, bootOpts)
	if err != nil {
		slog.Error("bootstrap failed", "error", err)
		return exitError
	}
	defer func() {
		if err := boot.Close(); err != nil {
			slog.Error("failed to close bootstrap resources", "error", err)
		}
	}()

	exitCode, err := inv.Runner.Run(ctx, boot)
	if err != nil {
		slog.Error("subcommand failed", "error", err)
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
	registerSubcommandFlags(fs, subcmd, &opts)

	if err := fs.Parse(args[1:]); err != nil {
		printUsage(stderr)
		return cliInvocation{}, err
	}
	if err := validateParsedFlags(subcmd, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		printUsage(stderr)
		return cliInvocation{}, err
	}
	return cliInvocation{Subcommand: subcmd, Options: opts, Runner: runner}, nil
}

func registerSubcommandFlags(fs *flag.FlagSet, subcmd SubcommandName, opts *cliOptions) {
	switch subcmd {
	case subcommandFetch:
		fs.StringVar(&opts.Since, "since", "", "fetch window duration")
	case subcommandSummary:
		fs.StringVar(&opts.Window, "window", "", "summary window duration")
	case subcommandGC:
		fs.StringVar(&opts.Before, "before", "", "report retention duration")
		fs.StringVar(&opts.MaxEmailAge, "max-email-age", "", "email retention duration")
	case subcommandRecover:
		fs.StringVar(&opts.RecoverMode, "mode", "", "recovery mode")
		fs.BoolVar(&opts.RecoverYes, "yes", false, "confirm recovery action")
		fs.BoolVar(&opts.RecoverAbort, "abort-reset", false, "abort pending reset")
	case subcommandReprocess:
		fs.BoolVar(&opts.ReprocessNotify, "notify", false, "send notifications during reprocess")
	}
}

func validateParsedFlags(subcmd SubcommandName, opts cliOptions) error {
	for name, value := range map[string]string{
		"since":         opts.Since,
		"window":        opts.Window,
		"before":        opts.Before,
		"max-email-age": opts.MaxEmailAge,
	} {
		if value == "" {
			continue
		}
		if _, err := ParseDuration(value); err != nil {
			return fmt.Errorf("invalid --%s: %w", name, err)
		}
	}
	if subcmd == subcommandRecover && opts.RecoverMode != "" {
		if opts.RecoverMode != "keep-old" && opts.RecoverMode != "discard-old" {
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
		subcommandFetch:     stubRunner{name: subcommandFetch},
		subcommandSummary:   stubRunner{name: subcommandSummary},
		subcommandReprocess: stubRunner{name: subcommandReprocess},
		subcommandGC:        stubRunner{name: subcommandGC},
		subcommandRecover:   stubRunner{name: subcommandRecover},
	}
}

type stubRunner struct {
	name SubcommandName
}

func (r stubRunner) Run(_ context.Context, _ *BootContext) (int, error) {
	slog.Info("subcommand runner is not implemented yet", "subcommand", r.name)
	return exitOK, nil
}
