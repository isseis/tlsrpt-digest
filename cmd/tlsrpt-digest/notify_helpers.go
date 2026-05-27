package main

import (
	"context"
	"log/slog"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// logAlerts logs one alert per failing policy in the report.
// component is used as the slog prefix (e.g. "fetch", "reprocess").
func logAlerts(ctx context.Context, notifier NotificationSink, report *tlsrpt.Report, component string) {
	for _, policy := range report.Policies {
		if policy.Summary.TotalFailureSessionCount <= 0 {
			continue
		}
		if err := notifier.LogAlert(ctx, notify.Alert{
			OrganizationName: report.OrganizationName,
			PolicyType:       notify.PolicyType(policy.Policy.PolicyType),
			FailureCount:     policy.Summary.TotalFailureSessionCount,
			DateRange: notify.DateRange{
				Start: report.DateRange.StartDatetime,
				End:   report.DateRange.EndDatetime,
			},
		}); err != nil {
			slog.Error(component+": log alert", "error", err)
		}
	}
}

// logWarn buffers a warning notification; logs errors from LogWarning but does not abort.
// component is used as the slog prefix (e.g. "fetch", "reprocess").
func logWarn(ctx context.Context, notifier NotificationSink, kind notify.WarningKind, uid, uidValidity uint32, messageID string, component string) {
	if notifier == nil {
		return
	}
	if err := notifier.LogWarning(ctx, notify.Warning{
		Kind:        kind,
		UID:         uid,
		UIDValidity: uidValidity,
		MessageID:   messageID,
	}); err != nil {
		slog.Error(component+": log warning", "error", err)
	}
}
