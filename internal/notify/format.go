package notify

import (
	"fmt"
	"log/slog"
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
// Exported for use in tests.
func TruncateText(s string, maxLen int) string {
	return truncateText(s, maxLen)
}

func truncateText(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen-len([]rune(truncSuffix))]) + truncSuffix
}

// truncateMessage applies Slack field-length limits to m in place.
// This must be called after DebugLogger logging so the debug output is untruncated.
func truncateMessage(m *slackMessage) {
	m.Text = truncateText(m.Text, maxTextRunes)
	for i := range m.Attachments {
		for j := range m.Attachments[i].Fields {
			m.Attachments[i].Fields[j].Value = truncateText(
				m.Attachments[i].Fields[j].Value, maxFieldRunes,
			)
		}
	}
}

// formatRecords converts a slice of slog.Records to a single slackMessage.
// Records are classified by level and message content.
func formatRecords(records []slog.Record, runID string) slackMessage {
	var alerts []Alert
	var sysErrors []SystemError
	var summaries []Summary

	for i := range records {
		r := records[i]
		switch {
		case r.Level >= slog.LevelError:
			sysErrors = append(sysErrors, extractSystemError(r))
		case r.Level == slog.LevelWarn:
			alerts = append(alerts, extractAlert(r))
		default:
			summaries = append(summaries, extractSummary(r))
		}
	}

	// Build a combined message. Priority: system errors > alerts > summary.
	if len(sysErrors) > 0 {
		return formatSystemErrorList(sysErrors, runID)
	}
	if len(alerts) > 0 {
		return formatAlerts(alerts, runID)
	}
	if len(summaries) > 0 {
		return formatSummary(summaries[0], runID)
	}
	return slackMessage{Text: fmt.Sprintf("notification (Run ID: %s)", runID)}
}

// extractAlert reads Alert fields from slog.Attrs stored by LogAlert.
func extractAlert(r slog.Record) Alert {
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
		}
		return true
	})
	return a
}

// extractSystemError reads SystemError fields from slog.Attrs stored by LogSystemError.
func extractSystemError(r slog.Record) SystemError {
	var e SystemError
	e.ErrorType = r.Message
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "message":
			e.Message = attr.Value.String()
		case "component":
			e.Component = attr.Value.String()
		}
		return true
	})
	return e
}

// extractSummary reads Summary fields from slog.Attrs stored by LogSummary.
func extractSummary(r slog.Record) Summary {
	var s Summary
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "organization_count":
			s.OrganizationCount = int(attr.Value.Int64())
		case "report_count":
			s.ReportCount = int(attr.Value.Int64())
		case "period_start":
			if t, ok := attr.Value.Any().(time.Time); ok {
				s.Period.Start = t
			}
		case "period_end":
			if t, ok := attr.Value.Any().(time.Time); ok {
				s.Period.End = t
			}
		}
		return true
	})
	return s
}

// formatAlerts builds a single aggregated slackMessage for TLS failure alerts.
// No truncation is applied here; the caller (Flush) truncates before sending.
func formatAlerts(alerts []Alert, runID string) slackMessage {
	title := fmt.Sprintf("%s TLS Failures – %d organizations affected", emojiAlert, len(alerts))

	var fields []slackField
	for _, a := range alerts {
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
	fields = append(fields, slackField{Title: "Run ID", Value: runID, Short: true})

	return slackMessage{
		Text: title,
		Attachments: []slackAttachment{
			{Color: colorWarning, Fields: fields},
		},
	}
}

// formatSystemErrorList builds a slackMessage for one or more system errors.
func formatSystemErrorList(errs []SystemError, runID string) slackMessage {
	if len(errs) == 1 {
		return formatSystemError(errs[0], runID)
	}
	// Multiple system errors: use first as representative, note count in title.
	title := fmt.Sprintf("%s System Errors (%d) – first: %s", emojiError, len(errs), errs[0].ErrorType)
	var fields []slackField
	for _, e := range errs {
		fields = append(fields, slackField{
			Title: "Error / Component",
			Value: fmt.Sprintf("%s | %s: %s", e.ErrorType, e.Component, e.Message),
			Short: false,
		})
	}
	fields = append(fields, slackField{Title: "Run ID", Value: runID, Short: true})
	return slackMessage{
		Text: title,
		Attachments: []slackAttachment{
			{Color: colorDanger, Fields: fields},
		},
	}
}

// formatSystemError builds a slackMessage for a single system error.
func formatSystemError(e SystemError, runID string) slackMessage {
	return slackMessage{
		Text: fmt.Sprintf("%s System Error: %s", emojiError, e.ErrorType),
		Attachments: []slackAttachment{
			{
				Color: colorDanger,
				Fields: []slackField{
					{Title: "Error", Value: e.Message, Short: false},
					{Title: "Component", Value: e.Component, Short: true},
					{Title: "Run ID", Value: runID, Short: true},
				},
			},
		},
	}
}

// formatSummary builds a slackMessage for a periodic summary.
func formatSummary(s Summary, runID string) slackMessage {
	return slackMessage{
		Text: fmt.Sprintf("%s TLS Report Summary", emojiSuccess),
		Attachments: []slackAttachment{
			{
				Color: colorGood,
				Fields: []slackField{
					{
						Title: "Period",
						Value: fmt.Sprintf("%s – %s",
							s.Period.Start.Format(time.DateOnly),
							s.Period.End.Format(time.DateOnly),
						),
						Short: true,
					},
					{Title: "Organizations", Value: fmt.Sprintf("%d", s.OrganizationCount), Short: true},
					{Title: "Reports", Value: fmt.Sprintf("%d", s.ReportCount), Short: true},
					{Title: "Run ID", Value: runID, Short: true},
				},
			},
		},
	}
}

// policyTypeStr returns the string representation, substituting a placeholder for unknown.
func policyTypeStr(pt PolicyType) string {
	if pt == PolicyTypeUnknown {
		return "(unknown)"
	}
	return strings.TrimSpace(string(pt))
}
