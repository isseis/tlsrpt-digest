package notify

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxTextRunes  = 4000
	maxFieldRunes = 1000
	truncSuffix   = "..."
)

// Color constants matching Slack's legacy attachment color scheme.
const (
	colorGood    = "good"
	colorWarning = "warning"
	colorDanger  = "danger"
)

// Emoji prefixes per notification type.
const (
	emojiAlert   = "⚠️"
	emojiError   = "🚨"
	emojiSuccess = "✅"
)

// TruncateText cuts s to at most maxLen runes. If truncation occurs, the result
// ends with "..." and its total rune count is exactly maxLen.
// Returns "" for maxLen <= 0.
func TruncateText(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	suffixLen := len([]rune(truncSuffix))
	if maxLen <= suffixLen {
		// maxLen too small to fit even the suffix; return suffix truncated to maxLen.
		return string([]rune(truncSuffix)[:maxLen])
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen-suffixLen]) + truncSuffix
}

// truncateMessage applies Slack field-length limits to m in place.
// This must be called after DebugLogger logging so the debug output is untruncated.
func truncateMessage(m *slackMessage) {
	m.Text = TruncateText(m.Text, maxTextRunes)
	for i := range m.Attachments {
		for j := range m.Attachments[i].Fields {
			m.Attachments[i].Fields[j].Value = TruncateText(
				m.Attachments[i].Fields[j].Value, maxFieldRunes,
			)
		}
	}
}

// formatRecords converts buffered slog.Records into one or more slackMessages.
// TLS failures are aggregated into a single message; fetch warnings each become
// individual messages; system errors become individual messages; summaries produce
// one message. The messages are ordered: TLS-failure aggregate (if any), fetch
// warnings (one each), system errors (one each), summary (if any).
// debugLogger receives warnings for unexpected attr keys; nil silences them.
func formatRecords(records []slog.Record, runID string, debugLogger *slog.Logger) []slackMessage {
	var alerts []Alert
	var warnings []Warning
	var sysErrors []SystemError
	var summaries []Summary

	for i := range records {
		r := records[i]
		switch {
		case r.Level >= slog.LevelError:
			sysErrors = append(sysErrors, extractSystemError(r, debugLogger))
		case r.Level >= slog.LevelWarn:
			if r.Message == "fetch_warning" {
				warnings = append(warnings, extractWarning(r, debugLogger))
			} else {
				alerts = append(alerts, extractAlert(r, debugLogger))
			}
		default:
			summaries = append(summaries, extractSummary(r, debugLogger))
		}
	}

	var msgs []slackMessage
	if len(alerts) > 0 {
		msgs = append(msgs, formatAlerts(alerts, runID))
	}
	for _, w := range warnings {
		msgs = append(msgs, formatWarning(w, runID))
	}
	for _, e := range sysErrors {
		msgs = append(msgs, formatSystemError(e, runID))
	}
	for _, s := range summaries {
		msgs = append(msgs, formatSummary(s, runID))
	}
	return msgs
}

// warnUnknownKey logs a warning when an unexpected attr key is encountered.
// Only the key name is logged; the value is omitted to avoid leaking sensitive data.
func warnUnknownKey(debugLogger *slog.Logger, key, recordMsg string) {
	if debugLogger != nil {
		debugLogger.Warn("unexpected attr key in notification record",
			"key", key, "record_message", recordMsg)
	}
}

// extractAlert reads Alert fields from slog.Attrs stored by LogAlert.
func extractAlert(r slog.Record, debugLogger *slog.Logger) Alert {
	var a Alert
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "organization_name":
			a.OrganizationName = attr.Value.String()
		case "policy_type":
			a.PolicyType = PolicyType(attr.Value.String())
		case "failure_count":
			a.FailureCount = attr.Value.Int64()
		case "date_start":
			if t, ok := attr.Value.Any().(time.Time); ok {
				a.DateRange.Start = t
			}
		case "date_end":
			if t, ok := attr.Value.Any().(time.Time); ok {
				a.DateRange.End = t
			}
		case "report_id":
			a.ReportID = attr.Value.String()
		case "failure_details_total_count":
			a.FailureDetailsTotalCount = attr.Value.Int64()
		case "failure_details_total_sessions":
			a.FailureDetailsTotalSessions = attr.Value.Int64()
		case "failure_details":
			if attr.Value.Kind() != slog.KindGroup {
				break
			}
			for _, child := range attr.Value.Group() {
				// Each child must be a named group (index "0", "1", ...).
				if child.Value.Kind() != slog.KindGroup {
					warnUnknownKey(debugLogger, "failure_details."+child.Key, r.Message)
					continue
				}
				var fd FailureDetail
				for _, field := range child.Value.Group() {
					switch field.Key {
					case "result_type":
						fd.ResultType = field.Value.String()
					case "failed_session_count":
						fd.FailedSessionCount = field.Value.Int64()
					case "receiving_mx_hostname":
						fd.ReceivingMXHostname = field.Value.String()
					case "failure_reason_code":
						fd.FailureReasonCode = field.Value.String()
					default:
						warnUnknownKey(debugLogger, "failure_details."+child.Key+"."+field.Key, r.Message)
					}
				}
				a.FailureDetails = append(a.FailureDetails, fd)
			}
		default:
			warnUnknownKey(debugLogger, attr.Key, r.Message)
		}
		return true
	})
	return a
}

// extractSystemError reads SystemError fields from slog.Attrs stored by LogSystemError.
func extractSystemError(r slog.Record, debugLogger *slog.Logger) SystemError {
	var e SystemError
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "kind":
			e.Kind = SystemErrorKind(attr.Value.String())
		case "component":
			e.Component = attr.Value.String()
		case "mailbox":
			e.Mailbox = attr.Value.String()
		default:
			warnUnknownKey(debugLogger, attr.Key, r.Message)
		}
		return true
	})
	return e
}

// extractWarning reads Warning fields from slog.Attrs stored by LogWarning.
func extractWarning(r slog.Record, debugLogger *slog.Logger) Warning {
	var w Warning
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "kind":
			w.Kind = WarningKind(attr.Value.String())
		case "uid":
			w.UID = uint32(attr.Value.Uint64()) //nolint:gosec // IMAP UIDs are defined as uint32 by RFC 3501
		case "uidvalidity":
			w.UIDValidity = uint32(attr.Value.Uint64()) //nolint:gosec // IMAP UIDVALIDITYs are uint32 by RFC 3501
		case "message_id":
			w.MessageID = attr.Value.String()
		default:
			warnUnknownKey(debugLogger, attr.Key, r.Message)
		}
		return true
	})
	return w
}

// extractSummary reads Summary fields from slog.Attrs stored by LogSummary.
func extractSummary(r slog.Record, debugLogger *slog.Logger) Summary {
	s := Summary{OrganizationStats: make(map[string]int64)}
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "report_count":
			s.ReportCount = attr.Value.Int64()
		case "period_start":
			if t, ok := attr.Value.Any().(time.Time); ok {
				s.Period.Start = t
			}
		case "period_end":
			if t, ok := attr.Value.Any().(time.Time); ok {
				s.Period.End = t
			}
		case "organization_stats":
			if attr.Value.Kind() == slog.KindGroup {
				for _, stat := range attr.Value.Group() {
					if stat.Value.Kind() != slog.KindInt64 {
						warnUnknownKey(debugLogger, "organization_stats."+stat.Key, r.Message)
						continue
					}
					s.OrganizationStats[stat.Key] = stat.Value.Int64()
				}
			}
		default:
			warnUnknownKey(debugLogger, attr.Key, r.Message)
		}
		return true
	})
	return s
}

// maxAlertFields caps alert fields per Slack attachment.
// Slack Incoming Webhooks allow at most 10 fields per attachment; one slot is
// reserved for the Run ID field appended to the last attachment.
const maxAlertFields = 9

// formatAlerts builds a single aggregated slackMessage for TLS failure alerts.
// Alerts are chunked across multiple attachments (≤9 alert fields each) to
// stay within Slack's 10-field-per-attachment limit.
// No truncation is applied here; the caller (Flush) truncates before sending.
func formatAlerts(alerts []Alert, runID string) slackMessage {
	orgCount := uniqueOrgCount(alerts)
	title := fmt.Sprintf("%s TLS Failures – %d organizations affected", emojiAlert, orgCount)

	var attachments []slackAttachment
	for i := 0; i < len(alerts); i += maxAlertFields {
		end := min(i+maxAlertFields, len(alerts))
		chunk := alerts[i:end]

		var fields []slackField
		for _, a := range chunk {
			fields = append(fields, slackField{
				Title: "Organization / Policy / Failures / Period",
				Value: fmt.Sprintf("%s | %s | %d | %s – %s",
					a.OrganizationName,
					policyTypeStr(a.PolicyType),
					a.FailureCount,
					a.DateRange.Start.Format(time.DateOnly),
					a.DateRange.End.Format(time.DateOnly),
				),
				Short: false,
			})
		}
		if end == len(alerts) {
			// Append Run ID only to the last attachment.
			fields = append(fields, slackField{Title: "Run ID", Value: runID, Short: true})
		}
		attachments = append(attachments, slackAttachment{Color: colorWarning, Fields: fields})
	}

	return slackMessage{
		Text:        title,
		Attachments: attachments,
	}
}

// formatSystemError builds a slackMessage for a single system error.
func formatSystemError(e SystemError, runID string) slackMessage {
	fields := []slackField{
		{Title: "Kind", Value: string(e.Kind), Short: true},
		{Title: "Component", Value: e.Component, Short: true},
	}
	if e.Mailbox != "" {
		fields = append(fields, slackField{Title: "Mailbox", Value: e.Mailbox, Short: true})
	}
	if hint := systemErrorHint(e.Kind); hint != "" {
		fields = append(fields, slackField{Title: "Action Required", Value: hint, Short: false})
	}
	fields = append(fields, slackField{Title: "Run ID", Value: runID, Short: true})
	return slackMessage{
		Text: fmt.Sprintf("%s System Error: %s", emojiError, string(e.Kind)),
		Attachments: []slackAttachment{
			{Color: colorDanger, Fields: fields},
		},
	}
}

// systemErrorHint returns an operator-facing action hint for the given SystemErrorKind.
// Returns "" for kinds that have no specific recovery action.
func systemErrorHint(kind SystemErrorKind) string {
	switch kind {
	case SystemErrorKindUIDValidityChanged, SystemErrorKindRecoveryRequired:
		return "Run: tlsrpt-digest --config <path> recover --mode discard-old --yes"
	case SystemErrorKindIMAPCredentialsMissing:
		return "Set TLSRPT_IMAP_USERNAME and TLSRPT_IMAP_PASSWORD environment variables"
	default:
		return ""
	}
}

// maxSummaryOrgFields caps organization fields per Slack attachment.
// The final attachment also receives the Run ID field.
const maxSummaryOrgFields = 9

// formatSummary builds a slackMessage for a periodic summary.
func formatSummary(s Summary, runID string) slackMessage {
	text := fmt.Sprintf("%s TLS Report Summary\nPeriod: %s – %s\nReports: %d\nOrganizations: %d",
		emojiSuccess,
		s.Period.Start.Format(time.DateOnly),
		s.Period.End.Format(time.DateOnly),
		s.ReportCount,
		len(s.OrganizationStats),
	)

	keys := slices.Sorted(maps.Keys(s.OrganizationStats))
	attachments := make([]slackAttachment, 0, max(1, (len(keys)+maxSummaryOrgFields-1)/maxSummaryOrgFields))
	if len(keys) == 0 {
		attachments = append(attachments, slackAttachment{
			Color:  colorGood,
			Fields: []slackField{{Title: "Run ID", Value: runID, Short: true}},
		})
		return slackMessage{Text: text, Attachments: attachments}
	}

	for i := 0; i < len(keys); i += maxSummaryOrgFields {
		end := min(i+maxSummaryOrgFields, len(keys))
		fields := make([]slackField, 0, maxSummaryOrgFields+1)
		for _, organization := range keys[i:end] {
			fields = append(fields, slackField{
				Title: organization,
				Value: fmt.Sprintf("%d successful sessions", s.OrganizationStats[organization]),
				Short: true,
			})
		}
		if end == len(keys) {
			fields = append(fields, slackField{Title: "Run ID", Value: runID, Short: true})
		}
		attachments = append(attachments, slackAttachment{Color: colorGood, Fields: fields})
	}

	return slackMessage{Text: text, Attachments: attachments}
}

// uniqueOrgCount returns the number of distinct OrganizationName values in alerts.
func uniqueOrgCount(alerts []Alert) int {
	seen := make(map[string]struct{}, len(alerts))
	for _, a := range alerts {
		seen[a.OrganizationName] = struct{}{}
	}
	return len(seen)
}

// policyTypeStr returns the string representation, substituting a placeholder for unknown.
func policyTypeStr(pt PolicyType) string {
	if pt == PolicyTypeUnknown {
		return "(unknown)"
	}
	return strings.TrimSpace(string(pt))
}
