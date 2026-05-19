// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"fmt"
	"time"
)

// SaveUIDValidity implements Store.SaveUIDValidity.
func (s *storeImpl) SaveUIDValidity(v uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	sentinel, _, err := loadSentinel(s.rootDir)
	if err != nil {
		return fmt.Errorf("SaveUIDValidity: load sentinel: %w", err)
	}
	if sentinel == nil {
		sentinel = &internalSentinelFile{
			FormatVersion: SentinelFormatVersion,
			IMAPHost:      s.identity.Host,
			IMAPPort:      s.identity.Port,
			IMAPMailbox:   s.identity.Mailbox,
		}
	}
	sentinel.UIDValidity = &v
	if err := saveSentinel(s.rootDir, sentinel); err != nil {
		return fmt.Errorf("SaveUIDValidity: save sentinel: %w", err)
	}
	s.sentinel = sentinel
	return nil
}

// LoadUIDValidity implements Store.LoadUIDValidity.
func (s *storeImpl) LoadUIDValidity() (v uint32, found bool, err error) {
	sentinel, exists, err := loadSentinel(s.rootDir)
	if err != nil {
		return 0, false, fmt.Errorf("LoadUIDValidity: load sentinel: %w", err)
	}
	if !exists || sentinel.UIDValidity == nil {
		return 0, false, nil
	}
	return *sentinel.UIDValidity, true, nil
}

// SaveRecoveryRequired implements Store.SaveRecoveryRequired.
func (s *storeImpl) SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error {
	if s.readOnly {
		return ErrReadOnly
	}
	sentinel, _, err := loadSentinel(s.rootDir)
	if err != nil {
		return fmt.Errorf("SaveRecoveryRequired: load sentinel: %w", err)
	}
	if sentinel == nil {
		sentinel = &internalSentinelFile{
			FormatVersion: SentinelFormatVersion,
			IMAPHost:      s.identity.Host,
			IMAPPort:      s.identity.Port,
			IMAPMailbox:   s.identity.Mailbox,
		}
	}
	sentinel.RecoveryRequired = &internalRecoveryState{
		PrevUIDValidity: prev,
		CurrUIDValidity: curr,
		DetectedAt:      detectedAt,
	}
	if err := saveSentinel(s.rootDir, sentinel); err != nil {
		return fmt.Errorf("SaveRecoveryRequired: save sentinel: %w", err)
	}
	s.sentinel = sentinel
	return nil
}

// LoadRecoveryRequired implements Store.LoadRecoveryRequired.
func (s *storeImpl) LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error) {
	sentinel, exists, err := loadSentinel(s.rootDir)
	if err != nil {
		return 0, 0, time.Time{}, false, fmt.Errorf("LoadRecoveryRequired: load sentinel: %w", err)
	}
	if !exists || sentinel.RecoveryRequired == nil {
		return 0, 0, time.Time{}, false, nil
	}
	rs := sentinel.RecoveryRequired
	return rs.PrevUIDValidity, rs.CurrUIDValidity, rs.DetectedAt, true, nil
}

// ClearRecoveryRequired implements Store.ClearRecoveryRequired.
func (s *storeImpl) ClearRecoveryRequired() error {
	if s.readOnly {
		return ErrReadOnly
	}
	sentinel, _, err := loadSentinel(s.rootDir)
	if err != nil {
		return fmt.Errorf("ClearRecoveryRequired: load sentinel: %w", err)
	}
	if sentinel == nil {
		return nil
	}
	sentinel.RecoveryRequired = nil
	if err := saveSentinel(s.rootDir, sentinel); err != nil {
		return fmt.Errorf("ClearRecoveryRequired: save sentinel: %w", err)
	}
	s.sentinel = sentinel
	return nil
}

// ApplyRecovery implements Store.ApplyRecovery.
func (s *storeImpl) ApplyRecovery(newUIDValidity uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	sentinel, _, err := loadSentinel(s.rootDir)
	if err != nil {
		return fmt.Errorf("ApplyRecovery: load sentinel: %w", err)
	}
	if sentinel == nil {
		sentinel = &internalSentinelFile{
			FormatVersion: SentinelFormatVersion,
			IMAPHost:      s.identity.Host,
			IMAPPort:      s.identity.Port,
			IMAPMailbox:   s.identity.Mailbox,
		}
	}
	sentinel.UIDValidity = &newUIDValidity
	sentinel.RecoveryRequired = nil
	if err := saveSentinel(s.rootDir, sentinel); err != nil {
		return fmt.Errorf("ApplyRecovery: save sentinel: %w", err)
	}
	s.sentinel = sentinel
	return nil
}
