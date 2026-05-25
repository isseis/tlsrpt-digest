//go:build test

package main

import (
	"context"

	"github.com/isseis/tlsrpt-digest/internal/notify"
)

type SpyNotificationSink struct {
	Alerts       []notify.Alert
	Warnings     []notify.Warning
	SystemErrors []notify.SystemError
	Summaries    []notify.Summary
	FlushCount   int
	DryRun       bool
	LogError     error
	FlushError   error
}

func (s *SpyNotificationSink) LogAlert(_ context.Context, alert notify.Alert) error {
	s.Alerts = append(s.Alerts, alert)
	return s.LogError
}

func (s *SpyNotificationSink) LogWarning(_ context.Context, warning notify.Warning) error {
	s.Warnings = append(s.Warnings, warning)
	return s.LogError
}

func (s *SpyNotificationSink) LogSystemError(_ context.Context, err notify.SystemError) error {
	s.SystemErrors = append(s.SystemErrors, err)
	return s.LogError
}

func (s *SpyNotificationSink) LogSummary(_ context.Context, summary notify.Summary) error {
	s.Summaries = append(s.Summaries, summary)
	return s.LogError
}

func (s *SpyNotificationSink) Flush(_ context.Context) error {
	s.FlushCount++
	return s.FlushError
}

func (s *SpyNotificationSink) IsDryRun() bool {
	return s.DryRun
}

var _ NotificationSink = (*SpyNotificationSink)(nil)
