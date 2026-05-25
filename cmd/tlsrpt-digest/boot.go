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
	Config       *config.Config
	Store        store.Store
	Notifier     NotificationSink
	LockHandle   LockHandle
	SummaryGuard store.SummaryConsistencyGuard
	Subcommand   SubcommandName
	Options      cliOptions
	RunID        string
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
	DryRun             bool
	RecoverResetMode   bool
	LoadConfig         func(path string) (*config.Config, error)
	BuildNotifier      func(successURL, errorURL config.Secret, cfg *config.Config, runID string, dryRun bool) (NotificationSink, error)
	AcquireWriterLock  func(rootDir string) (LockHandle, error)
	OpenStore          func(rootDir string, identity store.IMAPIdentity, mode store.OpenMode) (store.Store, error)
	Getenv             func(key string) string
	Stderr             *os.File
	SkipStoreOpen      bool
	SkipNotifierBuild  bool
	SkipWriterLock     bool
	SummaryGuardOpened func(store.SummaryConsistencyGuard)
}

var errSlackWebhookURLRequired = errors.New("at least one Slack webhook URL is required")

type notificationSinkImpl struct {
	handlers []*notify.SlackHandler
	dryRun   bool
}

func (n *notificationSinkImpl) LogAlert(ctx context.Context, alert notify.Alert) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogAlert(ctx, h, alert)
	})
}

func (n *notificationSinkImpl) LogWarning(ctx context.Context, warning notify.Warning) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogWarning(ctx, h, warning)
	})
}

func (n *notificationSinkImpl) LogSystemError(ctx context.Context, err notify.SystemError) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogSystemError(ctx, h, err)
	})
}

func (n *notificationSinkImpl) LogSummary(ctx context.Context, summary notify.Summary) error {
	return n.each(func(h *notify.SlackHandler) error {
		return notify.LogSummary(ctx, h, summary)
	})
}

func (n *notificationSinkImpl) Flush(ctx context.Context) error {
	return n.each(func(h *notify.SlackHandler) error {
		return h.Flush(ctx)
	})
}

func (n *notificationSinkImpl) IsDryRun() bool {
	return n != nil && n.dryRun
}

func (n *notificationSinkImpl) each(fn func(*notify.SlackHandler) error) error {
	if n == nil {
		return nil
	}
	var errs []error
	for _, h := range n.handlers {
		errs = append(errs, fn(h))
	}
	return errors.Join(errs...)
}

func Bootstrap(subcmd SubcommandName, configPath string, runID string, opts BootstrapOptions) (*BootContext, error) {
	opts = opts.withDefaults()
	cfg, err := opts.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	boot := &BootContext{
		Config:     cfg,
		Subcommand: subcmd,
		RunID:      runID,
	}
	defer func() {
		if err != nil {
			_ = boot.Close()
		}
	}()

	identity := storeIdentityFromConfig(cfg)
	if subcmd == subcommandSummary {
		if !opts.SkipStoreOpen {
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
		}
		return boot, nil
	}

	if !opts.SkipWriterLock {
		if err = validateAndEnsureRootDir(cfg.Store.RootDir); err != nil {
			return nil, fmt.Errorf("prepare store root: %w", err)
		}
	}

	if !opts.SkipNotifierBuild {
		successURL := config.Secret(opts.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS"))
		errorURL := config.Secret(opts.Getenv("TLSRPT_SLACK_WEBHOOK_URL_ERROR"))
		boot.Notifier, err = opts.BuildNotifier(successURL, errorURL, cfg, runID, opts.DryRun)
		if err != nil {
			return nil, fmt.Errorf("build notifier: %w", err)
		}
	}

	if !opts.SkipWriterLock {
		boot.LockHandle, err = opts.AcquireWriterLock(cfg.Store.RootDir)
		if err != nil {
			_ = notifyBootSystemError(context.Background(), boot.Notifier, notify.SystemErrorKindLockHeld, cfg)
			return nil, fmt.Errorf("acquire store writer lock: %w", err)
		}
	}

	if !opts.SkipStoreOpen {
		mode := storeOpenModeForBootstrap(subcmd, opts)
		boot.Store, err = opts.OpenStore(cfg.Store.RootDir, identity, mode)
		if err != nil {
			kind := classifyStoreOpenError(err)
			_ = notifyBootSystemError(context.Background(), boot.Notifier, kind, cfg)
			if errors.Is(err, store.ErrPendingReset) {
				return nil, fmt.Errorf("store reset is incomplete; run recover --mode discard-old --yes to continue or recover --abort-reset --yes to roll back: %w", err)
			}
			return nil, fmt.Errorf("open store: %w", err)
		}
	}

	return boot, nil
}

func (o BootstrapOptions) withDefaults() BootstrapOptions {
	if o.LoadConfig == nil {
		o.LoadConfig = loadConfig
	}
	if o.BuildNotifier == nil {
		o.BuildNotifier = setupNotifyHandlers
	}
	if o.AcquireWriterLock == nil {
		o.AcquireWriterLock = acquireStoreWriterLock
	}
	if o.OpenStore == nil {
		o.OpenStore = store.Open
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o
}

func notifyBootSystemError(ctx context.Context, notifier NotificationSink, kind notify.SystemErrorKind, cfg *config.Config) error {
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

func storeOpenModeForBootstrap(subcmd SubcommandName, opts BootstrapOptions) store.OpenMode {
	if subcmd == subcommandSummary {
		return store.OpenReadOnly
	}
	if subcmd == subcommandRecover && opts.RecoverResetMode {
		return store.OpenRecoverReset
	}
	return store.OpenReadWrite
}

func classifyStoreOpenError(err error) notify.SystemErrorKind {
	var identityErr *store.ErrStoreIdentityMismatch
	switch {
	case errors.Is(err, store.ErrPendingReset):
		return notify.SystemErrorKindResetIncomplete
	case errors.As(err, &identityErr):
		return notify.SystemErrorKindStoreIdentityMismatch
	case errors.Is(err, os.ErrPermission):
		return notify.SystemErrorKindStorePermission
	default:
		return notify.SystemErrorKindStoreCorruption
	}
}

func storeIdentityFromConfig(cfg *config.Config) store.IMAPIdentity {
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
	debugLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: debugLevel}))

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
	return &notificationSinkImpl{handlers: handlers, dryRun: dryRun}, nil
}

func loadConfig(path string) (*config.Config, error) {
	return config.LoadFile(path, slog.Default())
}
