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

	// UPSERT reports by report-id using a map for O(N) lookup.
	reportIdx := make(map[string]int, len(df.Reports))
	for i, r := range df.Reports {
		reportIdx[r.ReportID] = i
	}
	for _, input := range inputs {
		if i, ok := reportIdx[input.Report.ReportID]; ok {
			df.Reports[i] = input.Report
		} else {
			df.Reports = append(df.Reports, input.Report)
			reportIdx[input.Report.ReportID] = len(df.Reports) - 1
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

	result := make([]tlsrpt.Report, 0, len(df.Reports))
	for _, r := range df.Reports {
		// Include reports whose end-datetime is not before since (i.e., >= since).
		if !r.DateRange.EndDatetime.Before(since) {
			result = append(result, r)
		}
	}
	return result, nil
}

// DeleteReportsBefore implements Store.DeleteReportsBefore.
func (s *storeImpl) DeleteReportsBefore(cutoff time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}

	df, loadErr := s.loadDataFile()
	if loadErr != nil {
		return 0, fmt.Errorf("DeleteReportsBefore: load data file: %w", loadErr)
	}

	surviving := df.Reports[:0] // reuse backing array; write index <= read index
	for _, r := range df.Reports {
		if r.DateRange.EndDatetime.Before(cutoff) {
			deleted++
		} else {
			surviving = append(surviving, r)
		}
	}

	if deleted == 0 {
		return 0, nil
	}

	df.Reports = surviving
	if saveErr := s.saveDataFile(df); saveErr != nil {
		return 0, fmt.Errorf("DeleteReportsBefore: save data file: %w", saveErr)
	}

	return deleted, nil
}
