//go:build test

package store

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// makeTestIdentity returns a reusable IMAPIdentity for report tests.
func makeTestIdentity() IMAPIdentity {
	return IMAPIdentity{Host: "imap.example.com", Port: 993, Mailbox: "INBOX"}
}

// makeFullReport returns a tlsrpt.Report with every field populated, including
// failure-details, policy-string, and mx-host.
func makeFullReport(id string, endDate time.Time) tlsrpt.Report {
	return tlsrpt.Report{
		OrganizationName: "example.com",
		ReportID:         id,
		DateRange: tlsrpt.DateRange{
			StartDatetime: endDate.Add(-24 * time.Hour),
			EndDatetime:   endDate,
		},
		Policies: []tlsrpt.PolicyRecord{
			{
				Policy: tlsrpt.Policy{
					PolicyType:   "sts",
					PolicyString: []string{"version: STSv1", "mode: enforce"},
					PolicyDomain: "example.com",
					MXHost:       []string{"mail.example.com", "mx2.example.com"},
				},
				Summary: tlsrpt.Summary{
					TotalSuccessfulSessionCount: 100,
					TotalFailureSessionCount:    3,
				},
				FailureDetails: []tlsrpt.FailureDetail{
					{
						ResultType:            "certificate-expired",
						SendingMTAIP:          "192.0.2.1",
						ReceivingMXHostname:   "mail.example.com",
						ReceivingIP:           "198.51.100.1",
						FailedSessionCount:    3,
						AdditionalInformation: "cert expired 2025-01-01",
						FailureReasonCode:     "cert-exp",
					},
				},
			},
		},
	}
}

// openTestStore opens a read-write store in a temp dir.
func openTestStore(t *testing.T) (Store, string) {
	t.Helper()
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	return s, rootDir
}

// TestSaveReports_AllFieldsPreserved verifies that SaveReports persists all fields
// of a tlsrpt.Report, including failure-details, policy-string, and mx-host.
func TestSaveReports_AllFieldsPreserved(t *testing.T) {
	s, _ := openTestStore(t)
	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	original := makeFullReport("report-1", endDate)

	require.NoError(t, SaveReport(s, ReportInput{
		Report:      original,
		UID:         1,
		UIDValidity: 12345,
	}))

	reports, err := s.GetReportsSince(endDate)
	require.NoError(t, err)
	require.Len(t, reports, 1)

	got := reports[0]
	assert.Equal(t, original.ReportID, got.ReportID)
	assert.Equal(t, original.OrganizationName, got.OrganizationName)
	assert.Equal(t, original.DateRange.StartDatetime.Unix(), got.DateRange.StartDatetime.Unix())
	assert.Equal(t, original.DateRange.EndDatetime.Unix(), got.DateRange.EndDatetime.Unix())
	require.Len(t, got.Policies, 1)
	assert.Equal(t, original.Policies[0].Policy.PolicyType, got.Policies[0].Policy.PolicyType)
	assert.Equal(t, original.Policies[0].Policy.PolicyString, got.Policies[0].Policy.PolicyString)
	assert.Equal(t, original.Policies[0].Policy.MXHost, got.Policies[0].Policy.MXHost)
	require.Len(t, got.Policies[0].FailureDetails, 1)
	assert.Equal(t, original.Policies[0].FailureDetails[0], got.Policies[0].FailureDetails[0])
}

// TestSaveReports_Upsert verifies that saving the same report-id twice results
// in exactly one record (UPSERT semantics).
func TestSaveReports_Upsert(t *testing.T) {
	s, _ := openTestStore(t)
	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	r1 := makeFullReport("report-1", endDate)
	r2 := r1
	r2.OrganizationName = "updated.com"

	require.NoError(t, SaveReport(s, ReportInput{Report: r1, UID: 1, UIDValidity: 10}))
	require.NoError(t, SaveReport(s, ReportInput{Report: r2, UID: 1, UIDValidity: 10}))

	reports, err := s.GetReportsSince(endDate)
	require.NoError(t, err)
	require.Len(t, reports, 1, "duplicate report-id should result in exactly one record")
	assert.Equal(t, "updated.com", reports[0].OrganizationName)
}

// TestSaveReports_Batch verifies that SaveReports can save multiple reports
// in a single atomic call.
func TestSaveReports_Batch(t *testing.T) {
	s, _ := openTestStore(t)
	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	inputs := []ReportInput{
		{Report: makeFullReport("report-1", endDate), UID: 1, UIDValidity: 10},
		{Report: makeFullReport("report-2", endDate.Add(24*time.Hour)), UID: 2, UIDValidity: 10},
		{Report: makeFullReport("report-3", endDate.Add(48*time.Hour)), UID: 3, UIDValidity: 10},
	}
	require.NoError(t, s.SaveReports(inputs))

	since := endDate.Add(-time.Hour)
	reports, err := s.GetReportsSince(since)
	require.NoError(t, err)
	assert.Len(t, reports, 3)
}

// TestSaveReports_UpdatesReportEndDate verifies that when multiple reports share the
// same {uid, uidvalidity}, the email index entry's report_end_date is set to the maximum
// EndDatetime across all those reports.
func TestSaveReports_UpdatesReportEndDate(t *testing.T) {
	s, rootDir := openTestStore(t)

	earlier := time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC)
	later := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// Two reports for the same email, with different end dates.
	inputs := []ReportInput{
		{Report: makeFullReport("report-1", earlier), UID: 1, UIDValidity: 10},
		{Report: makeFullReport("report-2", later), UID: 1, UIDValidity: 10},
	}
	require.NoError(t, s.SaveReports(inputs))

	// Reload the data file and inspect the email index entry.
	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)
	require.NotNil(t, df.Emails[0].ReportEndDate)
	assert.True(t, df.Emails[0].ReportEndDate.Equal(later),
		"report_end_date should be the maximum EndDatetime")
}

// TestSaveReports_RoundTrip verifies that all fields of a tlsrpt.Report survive
// a full save → load round trip.
func TestSaveReports_RoundTrip(t *testing.T) {
	s, _ := openTestStore(t)
	endDate := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	original := makeFullReport("round-trip-report", endDate)

	require.NoError(t, SaveReport(s, ReportInput{
		Report:      original,
		UID:         99,
		UIDValidity: 777,
	}))

	reports, err := s.GetReportsSince(time.Time{})
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, original, reports[0])
}

// TestGetReportsSince_FilterSemantics verifies the boundary condition:
// EndDatetime == since is included; EndDatetime < since is excluded;
// EndDatetime > since is included.
func TestGetReportsSince_FilterSemantics(t *testing.T) {
	s, _ := openTestStore(t)

	since := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	before := since.Add(-time.Nanosecond)
	after := since.Add(time.Nanosecond)

	inputs := []ReportInput{
		{Report: makeFullReport("before", before), UID: 1, UIDValidity: 10},
		{Report: makeFullReport("equal", since), UID: 2, UIDValidity: 10},
		{Report: makeFullReport("after", after), UID: 3, UIDValidity: 10},
	}
	require.NoError(t, s.SaveReports(inputs))

	reports, err := s.GetReportsSince(since)
	require.NoError(t, err)

	// Only "equal" and "after" should be returned.
	ids := make(map[string]bool)
	for _, r := range reports {
		ids[r.ReportID] = true
	}
	assert.False(t, ids["before"], "report before cutoff should not be included")
	assert.True(t, ids["equal"], "report exactly at cutoff should be included")
	assert.True(t, ids["after"], "report after cutoff should be included")
	assert.Len(t, reports, 2)
}

// TestGetReportsSince_Empty verifies that an empty store returns a non-nil empty slice.
func TestGetReportsSince_Empty(t *testing.T) {
	s, _ := openTestStore(t)

	reports, err := s.GetReportsSince(time.Now())
	require.NoError(t, err)
	assert.NotNil(t, reports, "should return a non-nil empty slice")
	assert.Empty(t, reports)
}

// TestGetReportsSince_ReadOnly_Empty verifies that GetReportsSince on a read-only
// store backed by a non-existent file returns an empty slice without error.
func TestGetReportsSince_ReadOnly_Empty(t *testing.T) {
	rootDir := t.TempDir()
	nonexistent := rootDir + "/nosuchdir"
	s, err := Open(nonexistent, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	reports, err := s.GetReportsSince(time.Time{})
	require.NoError(t, err)
	assert.NotNil(t, reports)
	assert.Empty(t, reports)
}

// TestSaveReports_AtomicWrite verifies that no temporary files remain after SaveReports.
func TestSaveReports_AtomicWrite(t *testing.T) {
	s, rootDir := openTestStore(t)
	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("r1", endDate),
		UID:         1,
		UIDValidity: 10,
	}))

	entries, err := os.ReadDir(rootDir)
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		assert.False(t, len(name) > 4 && name[:4] == ".tmp",
			"no temp files should remain after SaveReports, found: %s", name)
	}
}

// TestSaveReports_Error verifies that SaveReports returns an error when the data
// file directory is read-only (simulating a write failure).
func TestSaveReports_Error(t *testing.T) {
	s, rootDir := openTestStore(t)

	// Make the root directory read-only so atomic write fails.
	require.NoError(t, os.Chmod(rootDir, 0o500))       //nolint:gosec
	t.Cleanup(func() { _ = os.Chmod(rootDir, 0o700) }) //nolint:gosec

	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	err := SaveReport(s, ReportInput{
		Report:      makeFullReport("r1", endDate),
		UID:         1,
		UIDValidity: 10,
	})
	assert.Error(t, err)
}

// TestSaveReports_UnsupportedVersion verifies that loading a data file with an
// unsupported version returns ErrUnsupportedSchemaVersion.
func TestSaveReports_UnsupportedVersion(t *testing.T) {
	_, rootDir := openTestStore(t)

	// Write a data file with an unsupported version.
	badData := []byte(`{"version":999,"reports":[],"emails":[]}`)
	require.NoError(t, os.WriteFile(dataFilePath(rootDir), badData, 0o600))

	// Re-open the store.
	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)

	// GetReportsSince should fail with an unsupported schema version error.
	_, err = s.GetReportsSince(time.Time{})
	require.Error(t, err)
	var schemaErr *ErrUnsupportedSchemaVersion
	require.ErrorAs(t, err, &schemaErr)
	assert.Equal(t, 999, schemaErr.Version)
}

// TestSaveReports_ReadOnlyReturnsError verifies that SaveReports returns ErrReadOnly
// when called on a read-only store.
func TestSaveReports_ReadOnlyReturnsError(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	err = SaveReport(s, ReportInput{Report: makeFullReport("r1", endDate), UID: 1, UIDValidity: 10})
	assert.ErrorIs(t, err, ErrReadOnly)
}

// loadDataFileFromPath is a test-only helper that loads the raw data file from a root dir.
func loadDataFileFromPath(rootDir string) (*internalDataFile, error) {
	impl := &storeImpl{
		dataPath: dataFilePath(rootDir),
	}
	return impl.loadDataFile()
}
