//go:build test

// Package storetestutil provides in-memory test doubles for the store package.
package storetestutil

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"path/filepath"
	"slices"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/store"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// EmailKey is the map key for the Emails index: (UID, UIDValidity) pair.
type EmailKey struct {
	UID         uint32
	UIDValidity uint32
}

// FakeEmailEntry holds the in-memory state for a single email in FakeStore.
type FakeEmailEntry struct {
	UID          uint32
	UIDValidity  uint32
	InternalDate time.Time
	RawEML       []byte
}

// FakeRecovery holds the recovery_required state stored by SaveRecoveryRequired.
type FakeRecovery struct {
	Prev       uint32
	Curr       uint32
	DetectedAt time.Time
}

// FakeStore is an in-memory implementation of store.Store for use in tests.
// All fields are exported so tests can inspect state directly.
type FakeStore struct {
	// Reports maps report-id to the stored report (UPSERT semantics).
	Reports map[string]tlsrpt.Report
	// UIDValidity holds the persisted UIDVALIDITY value; nil means not yet set.
	UIDValidity *uint32
	// Recovery holds the current recovery_required state; nil means no recovery.
	Recovery *FakeRecovery
	// Emails maps (UID, UIDValidity) to the stored email entry.
	Emails map[EmailKey]*FakeEmailEntry
	// PendingReset simulates a pending reset state for AbortReset testing.
	PendingReset bool
	// AcquireSummaryConsistencyGuardErr, if non-nil, is returned by AcquireSummaryConsistencyGuard.
	AcquireSummaryConsistencyGuardErr error

	// Error injection fields for individual operations.
	LoadRecoveryRequiredErr error
	SaveReportsErr          error
	SaveEmailMetasErr       error
	DeleteReportsBeforeErr  error
	DeleteEmailsBeforeErr   error
	LoadEmailsErr           error
	ApplyRecoveryErr        error
	ResetForRecoveryErr     error
	AbortResetErr           error

	// Call-count fields for ordering/invocation assertions.
	SaveEmailMetasCallCount      int
	SaveReportsCallCount         int
	LoadEmailsCallCount          int
	DeleteReportsBeforeCallCount int
	DeleteEmailsBeforeCallCount  int
	ApplyRecoveryCallCount       int
	ResetForRecoveryCallCount    int
	AbortResetCallCount          int

	// Cutoff capture fields for asserting the argument passed to delete operations.
	DeleteReportsCutoff time.Time
	DeleteEmailsCutoff  time.Time
}

// NewFakeStore returns an empty FakeStore ready for use.
func NewFakeStore() *FakeStore {
	return &FakeStore{
		Reports: make(map[string]tlsrpt.Report),
		Emails:  make(map[EmailKey]*FakeEmailEntry),
	}
}

// SaveReports implements store.Store.
func (f *FakeStore) SaveReports(inputs []store.ReportInput) error {
	f.SaveReportsCallCount++
	if f.SaveReportsErr != nil {
		return f.SaveReportsErr
	}
	for _, input := range inputs {
		f.Reports[input.Report.ReportID] = input.Report
	}
	return nil
}

// SaveEmailMetas implements store.Store.
func (f *FakeStore) SaveEmailMetas(metas []store.EmailMeta) error {
	f.SaveEmailMetasCallCount++
	if f.SaveEmailMetasErr != nil {
		return f.SaveEmailMetasErr
	}
	// Validate all entries before committing any, matching the real store's
	// atomic semantics (either all succeed or nothing is persisted).
	for _, meta := range metas {
		if meta.InternalDate.IsZero() {
			return store.ErrZeroInternalDate
		}
	}
	for _, meta := range metas {
		key := EmailKey{meta.UID, meta.UIDValidity}
		if _, ok := f.Emails[key]; ok {
			continue
		}
		f.Emails[key] = &FakeEmailEntry{
			UID:          meta.UID,
			UIDValidity:  meta.UIDValidity,
			InternalDate: meta.InternalDate,
		}
	}
	return nil
}

// GetAllReports implements store.Store.
// The returned slice is sorted by ReportID for deterministic ordering.
func (f *FakeStore) GetAllReports() ([]tlsrpt.Report, error) {
	result := make([]tlsrpt.Report, 0, len(f.Reports))
	for _, r := range f.Reports {
		result = append(result, r)
	}
	slices.SortFunc(result, func(a, b tlsrpt.Report) int {
		return cmp.Compare(a.ReportID, b.ReportID)
	})
	return result, nil
}

// SaveEmail implements store.Store.
func (f *FakeStore) SaveEmail(uid, uidValidity uint32, internalDate time.Time, rawEML []byte) error {
	if internalDate.IsZero() {
		return store.ErrZeroInternalDate
	}
	key := EmailKey{uid, uidValidity}
	if existing, ok := f.Emails[key]; ok && existing.RawEML != nil {
		return nil // idempotent
	}
	if _, ok := f.Emails[key]; !ok {
		f.Emails[key] = &FakeEmailEntry{UID: uid, UIDValidity: uidValidity}
	}
	f.Emails[key].InternalDate = internalDate
	rawCopy := make([]byte, len(rawEML))
	copy(rawCopy, rawEML)
	f.Emails[key].RawEML = rawCopy
	return nil
}

// LoadEmails implements store.Store.
func (f *FakeStore) LoadEmails() ([]store.LoadedEmail, error) {
	f.LoadEmailsCallCount++
	if f.LoadEmailsErr != nil {
		return nil, f.LoadEmailsErr
	}
	result := make([]store.LoadedEmail, 0, len(f.Emails))
	var errs []error

	for _, entry := range f.Emails {
		if entry.RawEML == nil {
			continue
		}
		msg, err := mail.ReadMessage(bytes.NewReader(entry.RawEML))
		if err != nil {
			errs = append(errs, &store.ErrLoadEmailFailed{
				Path:        fmt.Sprintf("%d/%d", entry.UIDValidity, entry.UID),
				UID:         entry.UID,
				UIDValidity: entry.UIDValidity,
				Err:         err,
			})
			continue
		}
		yyyymm := entry.InternalDate.UTC().Format("200601")
		relPath := filepath.Join(fmt.Sprintf("%d", entry.UIDValidity), yyyymm, fmt.Sprintf("%010d.eml", entry.UID))
		result = append(result, store.LoadedEmail{
			Message:      msg,
			UID:          entry.UID,
			UIDValidity:  entry.UIDValidity,
			InternalDate: entry.InternalDate,
			Path:         relPath,
		})
	}
	slices.SortFunc(result, func(a, b store.LoadedEmail) int {
		return cmp.Or(
			cmp.Compare(a.UIDValidity, b.UIDValidity),
			cmp.Compare(a.UID, b.UID),
		)
	})
	return result, errors.Join(errs...)
}

// SaveUIDValidity implements store.Store.
func (f *FakeStore) SaveUIDValidity(v uint32) error {
	vCopy := v
	f.UIDValidity = &vCopy
	return nil
}

// LoadUIDValidity implements store.Store.
func (f *FakeStore) LoadUIDValidity() (uint32, bool, error) {
	if f.UIDValidity == nil {
		return 0, false, nil
	}
	return *f.UIDValidity, true, nil
}

// SaveRecoveryRequired implements store.Store.
func (f *FakeStore) SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error {
	f.Recovery = &FakeRecovery{Prev: prev, Curr: curr, DetectedAt: detectedAt}
	return nil
}

// LoadRecoveryRequired implements store.Store.
func (f *FakeStore) LoadRecoveryRequired() (uint32, uint32, time.Time, bool, error) {
	if f.LoadRecoveryRequiredErr != nil {
		return 0, 0, time.Time{}, false, f.LoadRecoveryRequiredErr
	}
	if f.Recovery == nil {
		return 0, 0, time.Time{}, false, nil
	}
	return f.Recovery.Prev, f.Recovery.Curr, f.Recovery.DetectedAt, true, nil
}

// ClearRecoveryRequired implements store.Store.
func (f *FakeStore) ClearRecoveryRequired() error {
	f.Recovery = nil
	return nil
}

// ApplyRecovery implements store.Store.
func (f *FakeStore) ApplyRecovery(newUIDValidity uint32) error {
	f.ApplyRecoveryCallCount++
	if f.ApplyRecoveryErr != nil {
		return f.ApplyRecoveryErr
	}
	vCopy := newUIDValidity
	f.UIDValidity = &vCopy
	f.Recovery = nil
	return nil
}

// DeleteReportsBefore implements store.Store.
func (f *FakeStore) DeleteReportsBefore(cutoff time.Time) (int, error) {
	f.DeleteReportsBeforeCallCount++
	f.DeleteReportsCutoff = cutoff
	if f.DeleteReportsBeforeErr != nil {
		return 0, f.DeleteReportsBeforeErr
	}
	deleted := 0
	for id, r := range f.Reports {
		if r.DateRange.EndDatetime.Before(cutoff) {
			delete(f.Reports, id)
			deleted++
		}
	}
	return deleted, nil
}

// DeleteEmailsBefore implements store.Store.
func (f *FakeStore) DeleteEmailsBefore(cutoff time.Time) (int, error) {
	f.DeleteEmailsBeforeCallCount++
	f.DeleteEmailsCutoff = cutoff
	if f.DeleteEmailsBeforeErr != nil {
		return 0, f.DeleteEmailsBeforeErr
	}
	deleted := 0
	for key, entry := range f.Emails {
		if entry.InternalDate.Before(cutoff) {
			delete(f.Emails, key)
			deleted++
		}
	}
	return deleted, nil
}

// ResetForRecovery implements store.Store.
// Clears all reports and emails, sets UIDValidity to currUIDValidity, and
// clears Recovery. Returns ErrRecoveryRequiredMissing if Recovery is nil,
// or ErrRecoveryUIDValidityMismatch if currUIDValidity does not match.
func (f *FakeStore) ResetForRecovery(currUIDValidity uint32) error {
	f.ResetForRecoveryCallCount++
	if f.ResetForRecoveryErr != nil {
		return f.ResetForRecoveryErr
	}
	if f.Recovery == nil {
		return store.ErrRecoveryRequiredMissing
	}
	if f.Recovery.Curr != currUIDValidity {
		return &store.ErrRecoveryUIDValidityMismatch{Got: currUIDValidity, Expected: f.Recovery.Curr}
	}
	f.Reports = make(map[string]tlsrpt.Report)
	f.Emails = make(map[EmailKey]*FakeEmailEntry)
	vCopy := currUIDValidity
	f.UIDValidity = &vCopy
	f.Recovery = nil
	return nil
}

// HasPendingReset implements store.Store.
func (f *FakeStore) HasPendingReset() (bool, error) {
	return f.PendingReset, nil
}

// AbortReset implements store.Store.
// Returns ErrResetNotPending if there is no pending reset.
func (f *FakeStore) AbortReset() error {
	f.AbortResetCallCount++
	if f.AbortResetErr != nil {
		return f.AbortResetErr
	}
	if !f.PendingReset {
		return store.ErrResetNotPending
	}
	f.PendingReset = false
	return nil
}

// AcquireSummaryConsistencyGuard implements store.Store.
func (f *FakeStore) AcquireSummaryConsistencyGuard() (store.SummaryConsistencyGuard, error) {
	if f.AcquireSummaryConsistencyGuardErr != nil {
		return nil, f.AcquireSummaryConsistencyGuardErr
	}
	return &FakeSummaryConsistencyGuard{RecoveryRequiredFound: f.Recovery != nil}, nil
}

// FakeSummaryConsistencyGuard is a test double for store.SummaryConsistencyGuard.
// Set RecoveryRequiredFound to control the found return value of CheckRecoveryRequired.
// Set CheckError to inject an error return from CheckRecoveryRequired.
type FakeSummaryConsistencyGuard struct {
	RecoveryRequiredFound bool
	CheckError            error
	Closed                bool
}

// CheckRecoveryRequired implements store.SummaryConsistencyGuard.
func (g *FakeSummaryConsistencyGuard) CheckRecoveryRequired(_ context.Context) (bool, error) {
	if g.CheckError != nil {
		return false, g.CheckError
	}
	return g.RecoveryRequiredFound, nil
}

// Close implements store.SummaryConsistencyGuard.
func (g *FakeSummaryConsistencyGuard) Close() error {
	g.Closed = true
	return nil
}

// compile-time interface check
var _ store.Store = (*FakeStore)(nil)
