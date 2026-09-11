// Copyright 2026 Lasse Jellum and contributors. Licensed under Apache-2.0. See LICENSE.
//
// HAND-AUTHORED (NOVEL) — not generated, not overwritten by regen-merge.
//
// The per-(company, resource) sync watermark (issue #22). sync_state could not
// carry it: it is keyed by resource_type alone, so a `sync --company A` run
// would move the watermark past changes company B has not pulled yet, and the
// next unscoped run would skip them for good. A watermark that can be wrong in
// that direction is worse than none at all, so this table keys on both, and
// sync_state is left exactly as it was — `doctor`, the provenance envelope and
// the resume cursor all still read it.
//
// last_synced_at is TEXT holding RFC3339 in UTC, written by the CLI rather than
// by CURRENT_TIMESTAMP: the value is the START of the run that filled the
// window, not the moment the row was written, because rows keep changing while
// a walk runs. The reader (internal/cli/sync_watermark.go) widens it by a day
// before it reaches the wire.
package store

import (
	"database/sql"
	"fmt"
)

// syncWatermarkCreateSQL is the table, run from migrateExtras on every open.
const syncWatermarkCreateSQL = `CREATE TABLE IF NOT EXISTS sync_watermark (
	company_slug TEXT NOT NULL,
	resource_type TEXT NOT NULL,
	last_synced_at TEXT NOT NULL,
	PRIMARY KEY (company_slug, resource_type)
)`

// SyncWatermark returns the stored watermark for one (company, resource) as
// the RFC3339 string it was written as, or "" when no complete pull has ever
// finished for that pair — which the caller must read as "pull it in full".
//
// A store error answers "" too, deliberately: the conservative direction for a
// missing watermark is a full pull, and a sync must not fail over a
// bookkeeping row.
func (s *Store) SyncWatermark(companySlug, resourceType string) string {
	if companySlug == "" || resourceType == "" {
		return ""
	}
	var ts sql.NullString
	err := s.db.QueryRow(
		`SELECT last_synced_at FROM sync_watermark WHERE company_slug = ? AND resource_type = ?`,
		companySlug, resourceType,
	).Scan(&ts)
	if err != nil || !ts.Valid {
		return ""
	}
	return ts.String
}

// SetSyncWatermark records that everything changed up to lastSyncedAt has been
// pulled for one (company, resource). Callers must only call it after a pull
// they can vouch for as complete; the store cannot tell.
func (s *Store) SetSyncWatermark(companySlug, resourceType, lastSyncedAt string) error {
	if companySlug == "" || resourceType == "" {
		return fmt.Errorf("sync watermark needs both a company and a resource (got %q, %q)", companySlug, resourceType)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO sync_watermark (company_slug, resource_type, last_synced_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(company_slug, resource_type) DO UPDATE SET last_synced_at = excluded.last_synced_at`,
		companySlug, resourceType, lastSyncedAt,
	)
	return err
}

// ClearSyncWatermark drops the watermark for one (company, resource), so the
// next default run pulls that pair in full. This is what `sync --full` does
// before it refetches: the run is only allowed to write a new watermark from
// the pull it is about to make, never to leave an old one standing.
func (s *Store) ClearSyncWatermark(companySlug, resourceType string) error {
	if companySlug == "" || resourceType == "" {
		return fmt.Errorf("sync watermark needs both a company and a resource (got %q, %q)", companySlug, resourceType)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(
		`DELETE FROM sync_watermark WHERE company_slug = ? AND resource_type = ?`,
		companySlug, resourceType,
	)
	return err
}
