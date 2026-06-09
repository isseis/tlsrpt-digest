package main

import (
	"context"
	"log/slog"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// logAlerts logs one alert per failing policy in the report.
// component is used as the slog prefix (e.g. "fetch", "reprocess").
// Only the public 4 fields of each failure-detail entry are copied; IP addresses
// and additional-information are intentionally excluded to avoid leaking sensitive data.
func logAlerts(ctx context.Context, notifier NotificationSink, report *tlsrpt.Report, component string) {
	for _, policy := range report.Policies {
		if policy.Summary.TotalFailureSessionCount <= 0 {
			continue
		}
		details := make([]notify.FailureDetail, 0, len(policy.FailureDetails))
		for _, fd := range policy.FailureDetails {
			details = append(details, notify.FailureDetail{
				ResultType:          fd.ResultType,
				FailedSessionCount:  fd.FailedSessionCount,
				ReceivingMXHostname: fd.ReceivingMXHostname,
				FailureReasonCode:   fd.FailureReasonCode,
			})
		}
		if err := notifier.LogAlert(ctx, notify.Alert{
			OrganizationName: report.OrganizationName,
			PolicyType:       notify.PolicyType(policy.Policy.PolicyType),
			FailureCount:     policy.Summary.TotalFailureSessionCount,
			DateRange: notify.DateRange{
				Start: report.DateRange.StartDatetime,
				End:   report.DateRange.EndDatetime,
			},
			ReportID:       report.ReportID,
			FailureDetails: details,
		}); err != nil {
			slog.Warn(component+": log alert", "error", err)
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
		slog.Warn(component+": log warning", "error", err)
	}
}

func logNotifyError(message string, err error) {
	if err != nil {
		slog.Warn(message, "error", err)
	}
}
