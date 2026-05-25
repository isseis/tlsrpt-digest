package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/store"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// GenerateSummary aggregates successful TLSRPT reports in the period
// start <= report.DateRange.EndDatetime < end.
func GenerateSummary(ctx context.Context, st store.Store, start, end time.Time, debugLogger *slog.Logger) (Summary, error) {
	summary := Summary{
		Period:            DateRange{Start: start, End: end},
		OrganizationStats: make(map[string]int64),
	}

	if err := ctx.Err(); err != nil {
		return summary, fmt.Errorf("GenerateSummary: %w", err)
	}

	reports, err := st.GetAllReports()
	if err != nil {
		return summary, fmt.Errorf("GenerateSummary: %w", err)
	}

	for i := range reports {
		if err := ctx.Err(); err != nil {
			return summary, fmt.Errorf("GenerateSummary: %w", err)
		}

		report := reports[i]
		if !inSummaryPeriod(report, start, end) {
			continue
		}

		successfulSessions := successfulSessionCount(report)
		if report.HasFailure() {
			if successfulSessions > 0 && debugLogger != nil {
				debugLogger.WarnContext(ctx, "successful sessions in failed TLSRPT report excluded from summary",
					"organization_name", report.OrganizationName,
					"period_start", start,
					"period_end", end,
					"successful_session_count", successfulSessions,
				)
			}
			continue
		}

		summary.ReportCount++
		summary.OrganizationStats[report.OrganizationName] += successfulSessions
	}

	return summary, nil
}

func inSummaryPeriod(report tlsrpt.Report, start, end time.Time) bool {
	reportEnd := report.DateRange.EndDatetime
	return !reportEnd.Before(start) && reportEnd.Before(end)
}

func successfulSessionCount(report tlsrpt.Report) int64 {
	var total int64
	for i := range report.Policies {
		total += report.Policies[i].Summary.TotalSuccessfulSessionCount
	}
	return total
}
