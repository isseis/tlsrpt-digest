//go:build test

// Package storetestutil provides in-memory test doubles for the store package.
package storetestutil

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"net/mail"
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
	UID           uint32
	UIDValidity   uint32
	SentAt        time.Time
	SavedAt       time.Time
	RawEML        []byte
	ReportEndDate *time.Time
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
	for _, input := range inputs {
		f.Reports[input.Report.ReportID] = input.Report

		key := EmailKey{input.UID, input.UIDValidity}
		if _, ok := f.Emails[key]; !ok {
			f.Emails[key] = &FakeEmailEntry{UID: input.UID, UIDValidity: input.UIDValidity}
		}
		end := input.Report.DateRange.EndDatetime
		if f.Emails[key].ReportEndDate == nil || end.After(*f.Emails[key].ReportEndDate) {
			endCopy := end
			f.Emails[key].ReportEndDate = &endCopy
		}
	}
	return nil
}

// SaveEmailMetas implements store.Store.
func (f *FakeStore) SaveEmailMetas(metas []store.EmailMeta) error {
	for _, meta := range metas {
		sentAt := meta.SentAt
		if sentAt.IsZero() {
			sentAt = meta.SavedAt
		}

		key := EmailKey{meta.UID, meta.UIDValidity}
		if existing, ok := f.Emails[key]; ok {
			// Fill in SentAt/SavedAt only for placeholder entries (created by SaveReports
			// before SaveEmailMetas ran, so RawEML == nil).
			if existing.RawEML == nil {
				if existing.SentAt.IsZero() {
					existing.SentAt = sentAt
				}
				if existing.SavedAt.IsZero() {
					existing.SavedAt = meta.SavedAt
				}
			}
			continue
		}
		f.Emails[key] = &FakeEmailEntry{
			UID:         meta.UID,
			UIDValidity: meta.UIDValidity,
			SentAt:      sentAt,
			SavedAt:     meta.SavedAt,
		}
	}
	return nil
}

// GetReportsSince implements store.Store.
func (f *FakeStore) GetReportsSince(since time.Time) ([]tlsrpt.Report, error) {
	result := make([]tlsrpt.Report, 0, len(f.Reports))
	for _, r := range f.Reports {
		if !r.DateRange.EndDatetime.Before(since) {
			result = append(result, r)
		}
	}
	return result, nil
}

// SaveEmail implements store.Store.
func (f *FakeStore) SaveEmail(uid, uidValidity uint32, sentAt, savedAt time.Time, rawEML []byte) error {
	key := EmailKey{uid, uidValidity}
	if existing, ok := f.Emails[key]; ok && existing.RawEML != nil {
		return nil // idempotent
	}
	if _, ok := f.Emails[key]; !ok {
		f.Emails[key] = &FakeEmailEntry{UID: uid, UIDValidity: uidValidity}
	}
	f.Emails[key].SentAt = sentAt
	f.Emails[key].SavedAt = savedAt
	rawCopy := make([]byte, len(rawEML))
	copy(rawCopy, rawEML)
	f.Emails[key].RawEML = rawCopy
	return nil
}

// LoadEmails implements store.Store.
func (f *FakeStore) LoadEmails() ([]store.LoadedEmail, error) {
	result := make([]store.LoadedEmail, 0, len(f.Emails))
	var errs []error

	for _, entry := range f.Emails {
		if entry.RawEML == nil {
			continue
		}
		msg, err := mail.ReadMessage(bytes.NewReader(entry.RawEML))
		if err != nil {
			errs = append(errs, fmt.Errorf("FakeStore.LoadEmails: parse %d/%d: %w", entry.UIDValidity, entry.UID, err))
			continue
		}
		sentAt := entry.SavedAt
		if dateStr := msg.Header.Get("Date"); dateStr != "" {
			if t, parseErr := mail.ParseDate(dateStr); parseErr == nil {
				sentAt = t.UTC()
			}
		}
		dateForPath := entry.SentAt
		if dateForPath.IsZero() {
			dateForPath = entry.SavedAt
		}
		yyyymm := dateForPath.UTC().Format("200601")
		relPath := fmt.Sprintf("%d/%s/%010d.eml", entry.UIDValidity, yyyymm, entry.UID)
		result = append(result, store.LoadedEmail{
			Message:     msg,
			UID:         entry.UID,
			UIDValidity: entry.UIDValidity,
			SentAt:      sentAt,
			SavedAt:     entry.SavedAt,
			Path:        relPath,
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
	vCopy := newUIDValidity
	f.UIDValidity = &vCopy
	f.Recovery = nil
	return nil
}

// DeleteReportsBefore implements store.Store.
func (f *FakeStore) DeleteReportsBefore(cutoff time.Time) (int, error) {
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
func (f *FakeStore) DeleteEmailsBefore(reportCutoff, savedAtCutoff time.Time) (int, error) {
	deleted := 0
	for key, entry := range f.Emails {
		shouldDelete := false
		if entry.ReportEndDate != nil && entry.ReportEndDate.Before(reportCutoff) {
			shouldDelete = true
		}
		if !savedAtCutoff.IsZero() && !entry.SavedAt.IsZero() && entry.SavedAt.Before(savedAtCutoff) {
			shouldDelete = true
		}
		if shouldDelete {
			delete(f.Emails, key)
			deleted++
		}
	}
	return deleted, nil
}
