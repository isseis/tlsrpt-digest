package notify

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"
)

// LogAlert buffers a TLS failure alert record into h for delivery by Flush().
// It checks h.Enabled before calling h.Handle so LevelMode filtering is correct.
func LogAlert(ctx context.Context, h slog.Handler, alert Alert) error {
	if !h.Enabled(ctx, slog.LevelWarn) {
		return nil
	}
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "tls_failure_alert", 0)
	r.AddAttrs(
		slog.String("organization_name", alert.OrganizationName),
		slog.String("policy_type", string(alert.PolicyType)),
		slog.Int64("failure_count", alert.FailureCount),
		slog.Any("date_start", alert.DateRange.Start),
		slog.Any("date_end", alert.DateRange.End),
	)
	return h.Handle(ctx, r)
}

// LogSystemError buffers a system-level error record into h for delivery by Flush().
func LogSystemError(ctx context.Context, h slog.Handler, e SystemError) error {
	if !h.Enabled(ctx, slog.LevelError) {
		return nil
	}
	r := slog.NewRecord(time.Now(), slog.LevelError, e.ErrorType, 0)
	r.AddAttrs(
		slog.String("message", e.Message),
		slog.String("component", e.Component),
	)
	return h.Handle(ctx, r)
}

// LogSummary buffers a periodic summary record into h for delivery by Flush().
func LogSummary(ctx context.Context, h slog.Handler, s Summary) error {
	if !h.Enabled(ctx, slog.LevelInfo) {
		return nil
	}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "periodic_summary", 0)
	statAttrs := make([]any, 0, len(s.OrganizationStats))
	for _, organization := range slices.Sorted(maps.Keys(s.OrganizationStats)) {
		statAttrs = append(statAttrs, slog.Int64(organization, s.OrganizationStats[organization]))
	}
	r.AddAttrs(
		slog.Any("period_start", s.Period.Start),
		slog.Any("period_end", s.Period.End),
		slog.Int64("report_count", s.ReportCount),
		slog.Group("organization_stats", statAttrs...),
	)
	return h.Handle(ctx, r)
}
