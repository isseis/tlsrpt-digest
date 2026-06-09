package notify

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalSpyHandler records slog.Records for internal package tests.
type internalSpyHandler struct {
	records []slog.Record
}

func (s *internalSpyHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (s *internalSpyHandler) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r.Clone())
	return nil
}
func (s *internalSpyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return s }
func (s *internalSpyHandler) WithGroup(_ string) slog.Handler      { return s }

// TestLogAlert_FailureDetailsRoundTrip verifies the LogAlert → extractAlert round-trip
// preserves ReportID, sorts FailureDetails by FailedSessionCount descending, caps
// at 10 entries, and records exact total count and session totals from the full slice.
func TestLogAlert_FailureDetailsRoundTrip(t *testing.T) {
	period := DateRange{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
	}

	t.Run("report_id preserved", func(t *testing.T) {
		var spy internalSpyHandler
		require.NoError(t, LogAlert(context.Background(), &spy, Alert{
			OrganizationName: "example.com",
			PolicyType:       PolicyTypeSTS,
			FailureCount:     1,
			DateRange:        period,
			ReportID:         "rpt-abc-123",
		}))
		require.Len(t, spy.records, 1)
		got := extractAlert(spy.records[0], nil)
		assert.Equal(t, "rpt-abc-123", got.ReportID)
		assert.Nil(t, got.FailureDetails, "empty FailureDetails should round-trip as nil")
		assert.Equal(t, int64(0), got.FailureDetailsTotalCount)
		assert.Equal(t, int64(0), got.FailureDetailsTotalSessions)
	})

	t.Run("failure_details sorted descending and order preserved", func(t *testing.T) {
		details := []FailureDetail{
			{ResultType: "starttls-not-supported", FailedSessionCount: 2, ReceivingMXHostname: "mx1.example.com"},
			{ResultType: "certificate-expired", FailedSessionCount: 10, FailureReasonCode: "EXPIRED"},
			{ResultType: "validation-failure", FailedSessionCount: 5},
		}
		var spy internalSpyHandler
		require.NoError(t, LogAlert(context.Background(), &spy, Alert{
			OrganizationName: "example.com",
			PolicyType:       PolicyTypeSTS,
			FailureCount:     17,
			DateRange:        period,
			ReportID:         "rpt-order",
			FailureDetails:   details,
		}))
		require.Len(t, spy.records, 1)
		got := extractAlert(spy.records[0], nil)

		require.Len(t, got.FailureDetails, 3)
		// Expect descending order: 10, 5, 2
		assert.Equal(t, int64(10), got.FailureDetails[0].FailedSessionCount)
		assert.Equal(t, "certificate-expired", got.FailureDetails[0].ResultType)
		assert.Equal(t, "EXPIRED", got.FailureDetails[0].FailureReasonCode)
		assert.Equal(t, int64(5), got.FailureDetails[1].FailedSessionCount)
		assert.Equal(t, int64(2), got.FailureDetails[2].FailedSessionCount)
		assert.Equal(t, "mx1.example.com", got.FailureDetails[2].ReceivingMXHostname)
	})

	t.Run("failure_details exactly at cap boundary preserved", func(t *testing.T) {
		details := make([]FailureDetail, maxFailureDetails)
		for i := range details {
			details[i] = FailureDetail{ResultType: "error", FailedSessionCount: int64(i + 1)}
		}
		var spy internalSpyHandler
		require.NoError(t, LogAlert(context.Background(), &spy, Alert{
			OrganizationName: "example.com",
			PolicyType:       PolicyTypeSTS,
			FailureCount:     55,
			DateRange:        period,
			ReportID:         "rpt-boundary",
			FailureDetails:   details,
		}))
		require.Len(t, spy.records, 1)
		got := extractAlert(spy.records[0], nil)
		// All 10 entries should be preserved — none dropped at the boundary.
		assert.Len(t, got.FailureDetails, maxFailureDetails)
		assert.Equal(t, int64(maxFailureDetails), got.FailureDetailsTotalCount)
	})

	t.Run("failure_details capped at 10 with accurate totals", func(t *testing.T) {
		// Build 12 entries; totals should reflect all 12, not just the top 10.
		details := make([]FailureDetail, 12)
		var wantTotalSessions int64
		for i := range details {
			count := int64(i + 1) // 1..12
			details[i] = FailureDetail{ResultType: "error", FailedSessionCount: count}
			wantTotalSessions += count
		}
		var spy internalSpyHandler
		require.NoError(t, LogAlert(context.Background(), &spy, Alert{
			OrganizationName: "example.com",
			PolicyType:       PolicyTypeSTS,
			FailureCount:     wantTotalSessions,
			DateRange:        period,
			ReportID:         "rpt-cap",
			FailureDetails:   details,
		}))
		require.Len(t, spy.records, 1)
		got := extractAlert(spy.records[0], nil)

		// Only 10 entries preserved after cap.
		assert.Len(t, got.FailureDetails, 10)
		// Totals reflect all 12 entries (computed before cap).
		assert.Equal(t, int64(12), got.FailureDetailsTotalCount)
		assert.Equal(t, wantTotalSessions, got.FailureDetailsTotalSessions)
		// Top entry is the highest count (12).
		assert.Equal(t, int64(12), got.FailureDetails[0].FailedSessionCount)
	})
}

// TestTruncateMessage_Blocks exercises the block-level truncation path in
// truncateMessage: a section with text > 3000 runes is cut to 3000, and a
// divider block whose Text field is nil does not panic.
func TestTruncateMessage_Blocks(t *testing.T) {
	longText := strings.Repeat("a", maxAlertSectionRunes+100)
	longContext := strings.Repeat("b", maxAlertContextRunes+100)

	msg := &slackMessage{
		Text: "title",
		Attachments: []slackAttachment{
			{
				Color: colorWarning,
				Blocks: []slackBlock{
					// Section with text exceeding the section rune limit.
					{Type: "section", Text: &slackTextObject{Type: "plain_text", Text: longText}},
					// Divider with nil Text — must not panic.
					{Type: "divider"},
					// Context with text exceeding the context rune limit.
					{Type: "context", Elements: []slackTextObject{
						{Type: "plain_text", Text: longContext},
					}},
				},
			},
		},
	}

	require.NotPanics(t, func() { truncateMessage(msg) })

	blocks := msg.Attachments[0].Blocks
	require.Len(t, blocks, 3)

	sectionText := blocks[0].Text.Text
	assert.LessOrEqual(t, utf8.RuneCountInString(sectionText), maxAlertSectionRunes,
		"section text must be ≤ maxAlertSectionRunes after truncation")
	assert.True(t, strings.HasSuffix(sectionText, "..."), "truncated section must end with ...")

	assert.Nil(t, blocks[1].Text, "divider Text must still be nil after truncation")

	contextText := blocks[2].Elements[0].Text
	assert.LessOrEqual(t, utf8.RuneCountInString(contextText), maxAlertContextRunes,
		"context text must be ≤ maxAlertContextRunes after truncation")
}
