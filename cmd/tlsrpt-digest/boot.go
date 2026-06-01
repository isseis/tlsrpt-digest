package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/imap"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	"github.com/isseis/tlsrpt-digest/internal/storelock"
)

type SubcommandName string

const (
	subcommandFetch     SubcommandName = "fetch"
	subcommandSummary   SubcommandName = "summary"
	subcommandReprocess SubcommandName = "reprocess"
	subcommandGC        SubcommandName = "gc"
	subcommandRecover   SubcommandName = "recover"
)

type BootContext struct {
	Config                 *config.Config
	Store                  store.Store
	Notifier               NotificationSink
	LockHandle             LockHandle
	SummaryGuard           store.SummaryConsistencyGuard
	Subcommand             SubcommandName
	Options                cliOptions
	RunID                  string
	SlackWebhookURLSuccess config.Secret
	SlackWebhookURLError   config.Secret
}

func (b *BootContext) Close() error {
	if b == nil {
		return nil
	}
	var errs []error
	if b.SummaryGuard != nil {
		errs = append(errs, b.SummaryGuard.Close())
		b.SummaryGuard = nil
	}
	if b.LockHandle != nil {
		errs = append(errs, b.LockHandle.Close())
		b.LockHandle = nil
	}
	return errors.Join(errs...)
}

type IMAPCredentials struct {
	Username string
	Password config.Secret
}

type SubcommandRunner interface {
	Run(ctx context.Context, boot *BootContext) (exitCode int, err error)
}

type NotificationSink interface {
	LogAlert(ctx context.Context, alert notify.Alert) error
	LogWarning(ctx context.Context, warning notify.Warning) error
	LogSystemError(ctx context.Context, err notify.SystemError) error
	LogSummary(ctx context.Context, summary notify.Summary) error
	Flush(ctx context.Context) error
	IsDryRun() bool
}

type BootstrapOptions struct {
	DryRun                 bool
	Logger                 *slog.Logger
	LoadConfig             func(path string) (*config.Config, error)
	BuildNotifier          func(successURL, errorURL config.Secret, cfg *config.Config, runID string, dryRun bool) (NotificationSink, error)
	AcquireWriterLock      func(rootDir string) (LockHandle, error)
	OpenStore              func(rootDir string, identity store.IMAPIdentity, mode store.OpenMode) (store.Store, error)
	SlackWebhookURLSuccess config.Secret
	SlackWebhookURLError   config.Secret
	Stderr                 *os.File
	SummaryGuardOpened     func(store.SummaryConsistencyGuard)
	// StoreOpenModeOverride, when non-nil, overrides the default mode derived
	// from the subcommand name. Used by recover to select OpenReadWrite for
	// non-destructive modes and OpenRecoverReset for discard-old.
	StoreOpenModeOverride *store.OpenMode
}

var errSlackWebhookURLRequired = errors.New("at least one Slack webhook URL is required")

type nopNotifier struct{}

func (nopNotifier) LogAlert(_ context.Context, _ notify.Alert) error             { return nil }
func (nopNotifier) LogWarning(_ context.Context, _ notify.Warning) error         { return nil }
func (nopNotifier) LogSystemError(_ context.Context, _ notify.SystemError) error { return nil }
func (nopNotifier) LogSummary(_ context.Context, _ notify.Summary) error         { return nil }
func (nopNotifier) Flush(_ context.Context) error                                { return nil }
func (nopNotifier) IsDryRun() bool                                               { return false }

type notificationSink struct {
	handlers []*notify.SlackHandler
	dryRun   bool
}

func (n *notificationSink) LogAlert(ctx context.Context, alert notify.Alert) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogAlert(ctx, h, alert)
	})
}

func (n *notificationSink) LogWarning(ctx context.Context, warning notify.Warning) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogWarning(ctx, h, warning)
	})
}

func (n *notificationSink) LogSystemError(ctx context.Context, err notify.SystemError) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogSystemError(ctx, h, err)
	})
}

func (n *notificationSink) LogSummary(ctx context.Context, summary notify.Summary) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogSummary(ctx, h, summary)
	})
}

func (n *notificationSink) Flush(ctx context.Context) error {
	return n.each(func(h *notify.SlackHandler) error {
		return h.Flush(ctx)
	})
}

func (n *notificationSink) IsDryRun() bool {
	return n.dryRun
}

func (n *notificationSink) each(fn func(*notify.SlackHandler) error) error {
	var errs []error
	for _, h := range n.handlers {
		errs = append(errs, fn(h))
	}
	return errors.Join(errs...)
}

func Bootstrap(subcmd SubcommandName, configPath string, runID string, opts BootstrapOptions) (*BootContext, error) {
	opts = opts.withDefaults()
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default().With("run_id", runID)
	}

	var (
		cfg *config.Config
		err error
	)
	if opts.LoadConfig != nil {
		cfg, err = opts.LoadConfig(configPath)
	} else {
		cfg, err = loadConfig(configPath, logger)
	}
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	boot := &BootContext{
		Config:                 cfg,
		Subcommand:             subcmd,
		RunID:                  runID,
		SlackWebhookURLSuccess: opts.SlackWebhookURLSuccess,
		SlackWebhookURLError:   opts.SlackWebhookURLError,
	}
	defer func() {
		if err != nil {
			_ = boot.Close()
		}
	}()

	identity := storeIdentity(cfg)
	if subcmd == subcommandSummary {
		boot.Store, err = opts.OpenStore(cfg.Store.RootDir, identity, store.OpenReadOnly)
		if err != nil {
			return nil, fmt.Errorf("open summary store: %w", err)
		}
		boot.SummaryGuard, err = boot.Store.AcquireSummaryConsistencyGuard()
		if err != nil {
			return nil, fmt.Errorf("acquire summary consistency guard: %w", err)
		}
		if opts.SummaryGuardOpened != nil {
			opts.SummaryGuardOpened(boot.SummaryGuard)
		}
		return boot, nil
	}

	if err = validateAndEnsureRootDir(cfg.Store.RootDir); err != nil {
		return nil, fmt.Errorf("prepare store root: %w", err)
	}

	if subcmd == subcommandRecover {
		boot.Notifier = nopNotifier{}
	} else {
		boot.Notifier, err = opts.BuildNotifier(opts.SlackWebhookURLSuccess, opts.SlackWebhookURLError, cfg, runID, opts.DryRun)
		if err != nil {
			return nil, fmt.Errorf("build notifier: %w", err)
		}
	}

	boot.LockHandle, err = opts.AcquireWriterLock(cfg.Store.RootDir)
	if err != nil {
		kind := notify.SystemErrorKindStorePermission
		if errors.Is(err, storelock.ErrLockHeld) {
			kind = notify.SystemErrorKindLockHeld
		}
		if notifyErr := notifySystemError(context.Background(), boot.Notifier, kind, cfg); notifyErr != nil {
			slog.Warn("bootstrap: notify lock error", "error", notifyErr)
		}
		return nil, fmt.Errorf("acquire store writer lock: %w", err)
	}

	var mode store.OpenMode
	if opts.StoreOpenModeOverride != nil {
		mode = *opts.StoreOpenModeOverride
	} else {
		mode = storeOpenMode(subcmd)
	}
	boot.Store, err = opts.OpenStore(cfg.Store.RootDir, identity, mode)
	if err != nil {
		kind := classifyOpenError(err)
		if notifyErr := notifySystemError(context.Background(), boot.Notifier, kind, cfg); notifyErr != nil {
			slog.Warn("bootstrap: notify store open error", "error", notifyErr)
		}
		if errors.Is(err, store.ErrPendingReset) {
			return nil, fmt.Errorf("store reset is incomplete; run recover --mode discard-old --yes to continue: %w", err)
		}
		return nil, fmt.Errorf("open store: %w", err)
	}

	return boot, nil
}

func (o BootstrapOptions) withDefaults() BootstrapOptions {
	if o.BuildNotifier == nil {
		o.BuildNotifier = setupNotifyHandlers
	}
	if o.AcquireWriterLock == nil {
		// Bootstrap calls validateAndEnsureRootDir before invoking AcquireWriterLock, so only acquire the lock here.
		o.AcquireWriterLock = func(rootDir string) (LockHandle, error) {
			return storelock.Acquire(storelock.LockPath(rootDir))
		}
	}
	if o.OpenStore == nil {
		o.OpenStore = store.Open
	}
	if o.SlackWebhookURLSuccess == "" {
		o.SlackWebhookURLSuccess = config.Secret(os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS"))
	}
	if o.SlackWebhookURLError == "" {
		o.SlackWebhookURLError = config.Secret(os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_ERROR"))
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o
}

func notifySystemError(ctx context.Context, notifier NotificationSink, kind notify.SystemErrorKind, cfg *config.Config) error {
	if notifier == nil {
		return nil
	}
	err := notifier.LogSystemError(ctx, notify.SystemError{
		Kind:      kind,
		Component: "boot",
		Mailbox:   mailboxID(cfg),
	})
	return errors.Join(err, notifier.Flush(ctx))
}

func storeOpenMode(subcmd SubcommandName) store.OpenMode {
	if subcmd == subcommandSummary {
		return store.OpenReadOnly
	}
	return store.OpenReadWrite
}

func classifyOpenError(err error) notify.SystemErrorKind {
	if errors.Is(err, store.ErrPendingReset) {
		return notify.SystemErrorKindResetIncomplete
	}
	if _, ok := errors.AsType[*store.ErrStoreIdentityMismatch](err); ok {
		return notify.SystemErrorKindStoreIdentityMismatch
	}
	if errors.Is(err, os.ErrPermission) {
		return notify.SystemErrorKindStorePermission
	}
	return notify.SystemErrorKindStoreCorruption
}

func storeIdentity(cfg *config.Config) store.IMAPIdentity {
	return store.IMAPIdentity{
		Host:    cfg.IMAP.Host,
		Port:    cfg.IMAP.Port,
		Mailbox: cfg.IMAP.Mailbox,
	}
}

func mailboxID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d/%s", cfg.IMAP.Host, cfg.IMAP.Port, cfg.IMAP.Mailbox)
}

func buildIMAPConfig(cfg *config.Config, creds IMAPCredentials) imap.Config {
	return imap.Config{
		Host:            cfg.IMAP.Host,
		Port:            cfg.IMAP.Port,
		Mailbox:         cfg.IMAP.Mailbox,
		TLSCACert:       cfg.IMAP.TLSCACert,
		MaxMessageBytes: cfg.IMAP.MaxMessageBytes,
		Username:        creds.Username,
		Password:        creds.Password,
	}
}

func setupNotifyHandlers(successURL, errorURL config.Secret, cfg *config.Config, runID string, dryRun bool) (NotificationSink, error) {
	if !dryRun && successURL.Value() == "" && errorURL.Value() == "" {
		return nil, errSlackWebhookURLRequired
	}

	debugLevel := slog.LevelWarn
	if dryRun {
		debugLevel = slog.LevelDebug
	}
	debugLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: debugLevel})).With("run_id", runID)

	opts := notify.SlackHandlerOptions{
		AllowedHost:   cfg.Notify.Slack.AllowedHost,
		RunID:         runID,
		IsDryRun:      dryRun,
		DebugLogger:   debugLogger,
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	handlers, err := notify.BuildHandlers(successURL.Value(), errorURL.Value(), cfg.Notify.Slack.AllowedHost, opts)
	if err != nil {
		return nil, err
	}
	return &notificationSink{handlers: handlers, dryRun: dryRun}, nil
}

func defaultBuildSummaryNotifier(boot *BootContext) (NotificationSink, error) {
	return setupNotifyHandlers(boot.SlackWebhookURLSuccess, boot.SlackWebhookURLError, boot.Config, boot.RunID, boot.Options.DryRun)
}

func loadConfig(path string, logger *slog.Logger) (*config.Config, error) {
	if logger == nil {
		logger = slog.Default()
	}
	return config.LoadFile(path, logger)
}
