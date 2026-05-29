//go:build test

package store

import (
	"fmt"
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

	reports, err := s.GetAllReports()
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

	reports, err := s.GetAllReports()
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

	reports, err := s.GetAllReports()
	require.NoError(t, err)
	assert.Len(t, reports, 3)
}

// TestSaveReports_DoesNotUpdateEmailIndex verifies that SaveReports does not modify
// the email index (df.Emails remains empty after SaveReports).
func TestSaveReports_DoesNotUpdateEmailIndex(t *testing.T) {
	s, rootDir := openTestStore(t)

	endDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	inputs := []ReportInput{
		{Report: makeFullReport("report-1", endDate), UID: 1, UIDValidity: 10},
		{Report: makeFullReport("report-2", endDate.Add(24*time.Hour)), UID: 2, UIDValidity: 10},
	}
	require.NoError(t, s.SaveReports(inputs))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Empty(t, df.Emails, "SaveReports must not modify the email index")
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

	reports, err := s.GetAllReports()
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, original, reports[0])
}

// TestGetAllReports_ReturnsAllRegardlessOfDate verifies that GetAllReports returns
// all stored reports without any date filtering.
func TestGetAllReports_ReturnsAllRegardlessOfDate(t *testing.T) {
	s, _ := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	inputs := []ReportInput{
		{Report: makeFullReport("old", base.Add(-365*24*time.Hour)), UID: 1, UIDValidity: 10},
		{Report: makeFullReport("recent", base), UID: 2, UIDValidity: 10},
		{Report: makeFullReport("future", base.Add(365*24*time.Hour)), UID: 3, UIDValidity: 10},
	}
	require.NoError(t, s.SaveReports(inputs))

	reports, err := s.GetAllReports()
	require.NoError(t, err)
	require.Len(t, reports, 3, "GetAllReports should return all reports regardless of date")
	ids := make(map[string]bool, len(reports))
	for _, r := range reports {
		ids[r.ReportID] = true
	}
	assert.True(t, ids["old"], "old report should be included")
	assert.True(t, ids["recent"], "recent report should be included")
	assert.True(t, ids["future"], "future report should be included")
}

// TestGetAllReports_Empty verifies that an empty store returns a non-nil empty slice.
func TestGetAllReports_Empty(t *testing.T) {
	s, _ := openTestStore(t)

	reports, err := s.GetAllReports()
	require.NoError(t, err)
	assert.NotNil(t, reports, "should return a non-nil empty slice")
	assert.Empty(t, reports)
}

// TestGetAllReports_ReadOnly_Empty verifies that GetAllReports on a read-only
// store backed by a non-existent file returns an empty slice without error.
func TestGetAllReports_ReadOnly_Empty(t *testing.T) {
	rootDir := t.TempDir()
	nonexistent := rootDir + "/nosuchdir"
	s, err := Open(nonexistent, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	reports, err := s.GetAllReports()
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

	// GetAllReports should fail with an unsupported schema version error.
	_, err = s.GetAllReports()
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
	impl := &fileStore{
		dataPath: dataFilePath(rootDir),
	}
	return impl.loadDataFile()
}

// --- DeleteReportsBefore tests (Phase 3) ---

// TestDeleteReportsBefore_BoundaryValues verifies the cutoff boundary:
// end-datetime < cutoff is deleted; end-datetime == cutoff or > cutoff is kept.
func TestDeleteReportsBefore_BoundaryValues(t *testing.T) {
	s, _ := openTestStore(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Nanosecond)
	after := cutoff.Add(time.Nanosecond)

	inputs := []ReportInput{
		{Report: makeFullReport("before", before), UID: 1, UIDValidity: 10},
		{Report: makeFullReport("equal", cutoff), UID: 2, UIDValidity: 10},
		{Report: makeFullReport("after", after), UID: 3, UIDValidity: 10},
	}
	require.NoError(t, s.SaveReports(inputs))

	deleted, err := s.DeleteReportsBefore(cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	remaining, err := s.GetAllReports()
	require.NoError(t, err)
	ids := make(map[string]bool)
	for _, r := range remaining {
		ids[r.ReportID] = true
	}
	assert.False(t, ids["before"], "report before cutoff should be deleted")
	assert.True(t, ids["equal"], "report at cutoff should be kept")
	assert.True(t, ids["after"], "report after cutoff should be kept")
}

// TestDeleteReportsBefore_ZeroDeleted verifies that deleting 0 records returns deleted=0, err=nil.
func TestDeleteReportsBefore_ZeroDeleted(t *testing.T) {
	s, _ := openTestStore(t)

	deleted, err := s.DeleteReportsBefore(time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

// TestDeleteReportsBefore_Idempotent verifies that calling DeleteReportsBefore twice
// with the same cutoff gives 0 on the second call.
func TestDeleteReportsBefore_Idempotent(t *testing.T) {
	s, _ := openTestStore(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Hour)
	require.NoError(t, SaveReport(s, ReportInput{Report: makeFullReport("r1", before), UID: 1, UIDValidity: 10}))

	deleted1, err := s.DeleteReportsBefore(cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted1)

	deleted2, err := s.DeleteReportsBefore(cutoff)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted2)
}

// TestDeleteReportsBefore_AtomicWrite verifies that no temporary files remain after deletion.
func TestDeleteReportsBefore_AtomicWrite(t *testing.T) {
	s, rootDir := openTestStore(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("r1", cutoff.Add(-time.Hour)),
		UID:         1,
		UIDValidity: 10,
	}))

	_, err := s.DeleteReportsBefore(cutoff)
	require.NoError(t, err)

	entries, err := os.ReadDir(rootDir)
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		assert.False(t, len(name) > 4 && name[:4] == ".tmp",
			"no temp files should remain after DeleteReportsBefore, found: %s", name)
	}
}

// TestDeleteReportsBefore_Performance verifies that 10 000-record operations complete in < 1s.
func TestDeleteReportsBefore_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}
	s, _ := openTestStore(t)

	cutoff := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	const n = 10000
	inputs := make([]ReportInput, n)
	for i := range inputs {
		endDate := cutoff.Add(time.Duration(i-n/2) * time.Hour)
		r := tlsrpt.Report{
			ReportID:         fmt.Sprintf("perf-%d", i),
			OrganizationName: "perf.example.com",
			DateRange: tlsrpt.DateRange{
				StartDatetime: endDate.Add(-24 * time.Hour),
				EndDatetime:   endDate,
			},
		}
		inputs[i] = ReportInput{Report: r, UID: uint32(i + 1), UIDValidity: 10} //nolint:gosec
	}
	require.NoError(t, s.SaveReports(inputs))

	start := time.Now()
	_, err := s.GetAllReports()
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "GetAllReports should complete within 1s for 10k records")

	start = time.Now()
	_, err = s.DeleteReportsBefore(cutoff)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second, "DeleteReportsBefore should complete within 1s for 10k records")
}
