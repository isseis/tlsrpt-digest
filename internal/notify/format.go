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

// truncateMessage applies Slack length limits to m in place.
// This must be called after DebugLogger logging so the debug output is untruncated.
func truncateMessage(m *slackMessage) {
	m.Text = TruncateText(m.Text, maxTextRunes)
	for i := range m.Attachments {
		for j := range m.Attachments[i].Fields {
			m.Attachments[i].Fields[j].Value = TruncateText(
				m.Attachments[i].Fields[j].Value, maxFieldRunes,
			)
		}
		for j := range m.Attachments[i].Blocks {
			b := &m.Attachments[i].Blocks[j]
			if b.Text != nil {
				b.Text.Text = TruncateText(b.Text.Text, maxAlertSectionRunes)
			}
			for k := range b.Elements {
				b.Elements[k].Text = TruncateText(b.Elements[k].Text, maxAlertContextRunes)
			}
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
			children := attr.Value.Group()
			a.FailureDetails = make([]FailureDetail, 0, len(children))
			for _, child := range children {
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

// Block Kit size limits for alert messages.
const (
	maxAlertBlocksPerMessage  = 50   // Slack limit: 50 blocks per message
	maxAlertSectionRunes      = 3000 // Slack section text limit
	maxAlertContextRunes      = 300  // conservative limit for Run ID context
	maxAlertOrganizationRunes = 120
	maxAlertPolicyTypeRunes   = 80
	maxAlertReportIDRunes     = 160
	maxAlertResultTypeRunes   = 80
	maxAlertMXHostnameRunes   = 120
	maxAlertReasonCodeRunes   = 80

	// maxAlertDetailDisplay is the number of failure-detail entries shown in full;
	// additional entries are summarised as "Other N entries (M sessions total)".
	maxAlertDetailDisplay = 3

	// alertBlocksOverhead is the number of non-policy blocks reserved in
	// formatAlerts: 1 context + 1 overflow summary (when overflow occurs).
	alertBlocksOverhead = 2
)

// normalizeControlChars replaces ASCII control characters (< U+0020 or == U+007F)
// with a space. This prevents external values from injecting fake line breaks or
// other control sequences into plain_text block content.
func normalizeControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// formatAlerts builds a single aggregated slackMessage for TLS failure alerts
// using Block Kit sections. Each policy gets its own section block.
// No truncation is applied here; the caller (Flush) applies truncateMessage.
func formatAlerts(alerts []Alert, runID string) slackMessage {
	orgCount := uniqueOrgCount(alerts)
	title := fmt.Sprintf("%s TLS Failures – %d organizations affected", emojiAlert, orgCount)

	// 1 block reserved for context; when overflow is needed, also 1 for summary.
	const maxWithoutOverflow = maxAlertBlocksPerMessage - 1 // 49 policies + 1 context
	const maxWithOverflow = maxAlertBlocksPerMessage - 2    // 48 policies + 1 summary + 1 context

	shown := alerts
	var overflowAlerts []Alert
	if len(alerts) > maxWithoutOverflow {
		shown = alerts[:maxWithOverflow]
		overflowAlerts = alerts[maxWithOverflow:]
	}

	blocks := make([]slackBlock, 0, len(shown)+alertBlocksOverhead)
	for _, a := range shown {
		text := buildPolicySectionText(a)
		blocks = append(blocks, slackBlock{
			Type: "section",
			Text: &slackTextObject{Type: "plain_text", Text: text},
		})
	}

	if len(overflowAlerts) > 0 {
		overflowOrgs := uniqueOrgCount(overflowAlerts)
		var overflowSessions int64
		for _, a := range overflowAlerts {
			overflowSessions += a.FailureCount
		}
		overflowText := fmt.Sprintf("%d additional policies omitted; organizations: %d; failed sessions: %d",
			len(overflowAlerts), overflowOrgs, overflowSessions)
		blocks = append(blocks, slackBlock{
			Type: "section",
			Text: &slackTextObject{Type: "plain_text", Text: overflowText},
		})
	}

	// Append Run ID context block.
	blocks = append(blocks, slackBlock{
		Type:     "context",
		Elements: []slackTextObject{{Type: "plain_text", Text: "Run ID: " + runID}},
	})

	return slackMessage{
		Text: title,
		Attachments: []slackAttachment{
			{Color: colorWarning, Blocks: blocks},
		},
	}
}

// buildPolicySectionText constructs the plain_text content for one policy section.
// External-origin strings are control-char-normalized and per-field truncated
// before being assembled into the final text.
// Invariant: a.FailureDetailsTotalCount >= int64(len(a.FailureDetails)) — both are
// set by LogAlert from the same slice, with FailureDetailsTotalCount computed before
// the 10-entry cap. Direct Alert construction must respect this invariant for the
// "Other N entries" summary to be accurate.
func buildPolicySectionText(a Alert) string {
	orgName := TruncateText(normalizeControlChars(a.OrganizationName), maxAlertOrganizationRunes)
	policyType := TruncateText(normalizeControlChars(policyTypeStr(a.PolicyType)), maxAlertPolicyTypeRunes)
	reportID := TruncateText(normalizeControlChars(a.ReportID), maxAlertReportIDRunes)

	var sb strings.Builder
	// Line 1: org | policy type
	fmt.Fprintf(&sb, "%s | %s\n", orgName, policyType)
	// Line 2: failed sessions | period (UTC)
	fmt.Fprintf(&sb, "Failed sessions: %d | Period: %s – %s\n",
		a.FailureCount,
		a.DateRange.Start.UTC().Format(time.DateOnly),
		a.DateRange.End.UTC().Format(time.DateOnly),
	)
	// Line 3: Report ID
	fmt.Fprintf(&sb, "Report ID: %s", reportID)

	// Failure details (sorted descending by FailedSessionCount, already done in LogAlert).
	details := a.FailureDetails
	if len(details) > 0 {
		shown := details
		if len(shown) > maxAlertDetailDisplay {
			shown = shown[:maxAlertDetailDisplay]
		}
		sb.WriteByte('\n')
		for i, fd := range shown {
			resultType := TruncateText(normalizeControlChars(fd.ResultType), maxAlertResultTypeRunes)
			line := fmt.Sprintf("[%d] %s: %d sessions", i+1, resultType, fd.FailedSessionCount)
			if fd.ReceivingMXHostname != "" {
				mx := TruncateText(normalizeControlChars(fd.ReceivingMXHostname), maxAlertMXHostnameRunes)
				line += " | MX: " + mx
			}
			if fd.FailureReasonCode != "" {
				reason := TruncateText(normalizeControlChars(fd.FailureReasonCode), maxAlertReasonCodeRunes)
				line += " | Reason: " + reason
			}
			sb.WriteString(line)
			if i < len(shown)-1 || a.FailureDetailsTotalCount > maxAlertDetailDisplay {
				sb.WriteByte('\n')
			}
		}
		if a.FailureDetailsTotalCount > maxAlertDetailDisplay {
			var topSessions int64
			for _, fd := range shown {
				topSessions += fd.FailedSessionCount
			}
			otherCount := a.FailureDetailsTotalCount - maxAlertDetailDisplay
			otherSessions := a.FailureDetailsTotalSessions - topSessions
			fmt.Fprintf(&sb, "Other %d entries (%d sessions total)", otherCount, otherSessions)
		}
	}

	return sb.String()
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
