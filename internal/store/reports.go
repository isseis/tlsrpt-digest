// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// loadDataFile loads and parses the data file (tlsrpt.json).
// If the file does not exist, it returns an empty data file (for read-only mode).
func (s *storeImpl) loadDataFile() (*internalDataFile, error) {
	// G304: s.dataPath is derived from an application-controlled path.
	data, err := os.ReadFile(s.dataPath) //nolint:gosec
	if errors.Is(err, os.ErrNotExist) {
		// Treat missing file as empty state (used in read-only mode when no file exists).
		return &internalDataFile{
			Version: DataFileVersion,
			Reports: []tlsrpt.Report{},
			Emails:  []internalEmailIndexEntry{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loadDataFile: read: %w", err)
	}

	var df internalDataFile
	if err := json.Unmarshal(data, &df); err != nil {
		return nil, fmt.Errorf("loadDataFile: unmarshal: %w", err)
	}

	if df.Version != DataFileVersion {
		return nil, &ErrUnsupportedSchemaVersion{
			File:    s.dataPath,
			Version: df.Version,
		}
	}

	return &df, nil
}

// saveDataFile serializes and writes the data file atomically.
func (s *storeImpl) saveDataFile(df *internalDataFile) error {
	data, err := json.Marshal(df)
	if err != nil {
		return fmt.Errorf("saveDataFile: marshal: %w", err)
	}
	return atomicWriteFile(s.dataPath, data)
}

// SaveReports implements Store.SaveReports.
func (s *storeImpl) SaveReports(inputs []ReportInput) error {
	if s.readOnly {
		return ErrReadOnly
	}

	df, err := s.loadDataFile()
	if err != nil {
		return fmt.Errorf("SaveReports: load data file: %w", err)
	}

	// UPSERT reports by report-id.
	for _, input := range inputs {
		upserted := false
		for i, existing := range df.Reports {
			if existing.ReportID == input.Report.ReportID {
				df.Reports[i] = input.Report
				upserted = true
				break
			}
		}
		if !upserted {
			df.Reports = append(df.Reports, input.Report)
		}
	}

	// Compute the maximum EndDatetime per {uid, uidvalidity} across the current batch.
	type emailKey struct {
		UID         uint32
		UIDValidity uint32
	}
	maxEndDate := make(map[emailKey]time.Time)
	for _, input := range inputs {
		key := emailKey{UID: input.UID, UIDValidity: input.UIDValidity}
		if t, ok := maxEndDate[key]; !ok || input.Report.DateRange.EndDatetime.After(t) {
			maxEndDate[key] = input.Report.DateRange.EndDatetime
		}
	}

	// Update the report_end_date for each email index entry in the batch.
	// If the entry does not yet exist, create a minimal one so that the
	// report_end_date is not lost when SaveEmailMetas is called afterwards.
	for key, maxDate := range maxEndDate {
		maxDateCopy := maxDate
		found := false
		for i, entry := range df.Emails {
			if entry.UID == key.UID && entry.UIDValidity == key.UIDValidity {
				// Only advance the date (conservative GC semantics).
				if df.Emails[i].ReportEndDate == nil || maxDateCopy.After(*df.Emails[i].ReportEndDate) {
					df.Emails[i].ReportEndDate = &maxDateCopy
				}
				found = true
				break
			}
		}
		if !found {
			// Create a minimal index entry; sent_at/saved_at will be filled by SaveEmailMetas.
			df.Emails = append(df.Emails, internalEmailIndexEntry{
				UID:           key.UID,
				UIDValidity:   key.UIDValidity,
				ReportEndDate: &maxDateCopy,
			})
		}
	}

	return s.saveDataFile(df)
}

// GetReportsSince implements Store.GetReportsSince.
func (s *storeImpl) GetReportsSince(since time.Time) ([]tlsrpt.Report, error) {
	df, err := s.loadDataFile()
	if err != nil {
		return nil, fmt.Errorf("GetReportsSince: load data file: %w", err)
	}

	result := []tlsrpt.Report{}
	for _, r := range df.Reports {
		// Include reports whose end-datetime is not before since (i.e., >= since).
		if !r.DateRange.EndDatetime.Before(since) {
			result = append(result, r)
		}
	}
	return result, nil
}

// DeleteReportsBefore implements Store.DeleteReportsBefore.
// TODO: Phase 3 implementation
func (s *storeImpl) DeleteReportsBefore(_ time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	return 0, errNotImplemented
}
