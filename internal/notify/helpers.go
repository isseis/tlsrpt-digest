package notify

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"
)

// maxFailureDetails is the maximum number of FailureDetail entries encoded per alert.
// Total count and session totals are aggregated from the full slice before this cap.
const maxFailureDetails = 10

// LogAlert buffers a TLS failure alert record into h for delivery by Flush().
// It checks h.Enabled before calling h.Handle so LevelMode filtering is correct.
// FailureDetails are sorted by FailedSessionCount descending and capped at maxFailureDetails
// before encoding; total count and sessions are aggregated from the full slice first.
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
		slog.String("report_id", alert.ReportID),
	)

	// Aggregate totals from the full FailureDetails slice before applying the cap.
	var totalCount, totalSessions int64
	for _, fd := range alert.FailureDetails {
		totalCount++
		totalSessions += fd.FailedSessionCount
	}
	r.AddAttrs(
		slog.Int64("failure_details_total_count", totalCount),
		slog.Int64("failure_details_total_sessions", totalSessions),
	)

	// Sort failure details by FailedSessionCount descending, then cap to 10 entries.
	details := slices.Clone(alert.FailureDetails)
	slices.SortFunc(details, func(a, b FailureDetail) int {
		return cmp.Compare(b.FailedSessionCount, a.FailedSessionCount)
	})
	if len(details) > maxFailureDetails {
		details = details[:maxFailureDetails]
	}
	detailAttrs := make([]any, 0, len(details))
	for i, fd := range details {
		childAttrs := []any{
			slog.String("result_type", fd.ResultType),
			slog.Int64("failed_session_count", fd.FailedSessionCount),
			slog.String("receiving_mx_hostname", fd.ReceivingMXHostname),
			slog.String("failure_reason_code", fd.FailureReasonCode),
		}
		detailAttrs = append(detailAttrs, slog.Group(fmt.Sprintf("%d", i), childAttrs...))
	}
	r.AddAttrs(slog.Group("failure_details", detailAttrs...))

	return h.Handle(ctx, r)
}

// LogSystemError buffers a system-level error record into h for delivery by Flush().
func LogSystemError(ctx context.Context, h slog.Handler, e SystemError) error {
	if !h.Enabled(ctx, slog.LevelError) {
		return nil
	}
	r := slog.NewRecord(time.Now(), slog.LevelError, "system_error", 0)
	r.AddAttrs(
		slog.String("kind", string(e.Kind)),
		slog.String("component", e.Component),
	)
	if e.Mailbox != "" {
		r.AddAttrs(slog.String("mailbox", e.Mailbox))
	}
	return h.Handle(ctx, r)
}

// LogWarning buffers a fetch warning record into h for delivery by Flush().
// It uses WARN level so it is routed to the error webhook buffer alongside alerts.
func LogWarning(ctx context.Context, h slog.Handler, warning Warning) error {
	if !h.Enabled(ctx, slog.LevelWarn) {
		return nil
	}
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "fetch_warning", 0)
	r.AddAttrs(
		slog.String("kind", string(warning.Kind)),
		slog.Uint64("uid", uint64(warning.UID)),
		slog.Uint64("uidvalidity", uint64(warning.UIDValidity)),
		slog.String("message_id", warning.MessageID),
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
